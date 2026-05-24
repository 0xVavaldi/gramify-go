// Package runfile provides the on-disk run encoding and bounded k-way merge
// shared by the external-sort pipelines (freqsort and the accurate stdout
// dedup writer).
//
// A run is a sequence of (key, count) records, each encoded as
// uvarint(len(key)) | key bytes | uvarint(count). Merge opens every run at
// once but sizes the per-run read buffers to a byte budget, so merging stays
// within a fixed memory ceiling regardless of how many runs there are — which
// is what lets a small-RAM machine merge a large spilled dataset.
package runfile

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Record is one (key, count) pair, the unit of every run file.
type Record struct {
	Key   string
	Count uint64
	// Sort is an optional precomputed comparison key. Merge fills it from its
	// keyFn (when non-nil) once per record as the record is read, letting a
	// caller's less func order by an expensive-to-derive key (e.g. a case fold)
	// without recomputing it on every heap comparison. It is not persisted.
	Sort string
}

// read-buffer clamps for the merge phase.
const (
	minReadBuf = 4 << 10 // 4 KiB
	maxReadBuf = 1 << 20 // 1 MiB
	writeBuf   = 1 << 20 // spill writer buffer (one open at a time)
)

// maxFanIn caps how many runs a single merge pass opens at once. A single pass
// is always cheaper than cascading (which rewrites all data per extra pass), so
// this is set high: cascading is a safety valve for pathological run counts
// (thousands), where a single pass would exhaust file handles or collapse each
// per-run read buffer below minReadBuf. Below this, Merge does one pass.
const maxFanIn = 256

// Write creates a run file at path and drives the caller's record production
// through an emit callback that encodes each record.
//
// Args:
//
//	path:    destination file (created/truncated).
//	produce: called once with an emit(key, count) callback; the caller must emit
//	         records already ordered by whatever comparator Merge will use.
//
// Returns:
//
//	the first create/encode/flush error; nil on success.
func Write(path string, produce func(emit func(key string, count uint64) error) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, writeBuf)
	var tmp [binary.MaxVarintLen64]byte
	emit := func(key string, count uint64) error {
		n := binary.PutUvarint(tmp[:], uint64(len(key)))
		if _, e := w.Write(tmp[:n]); e != nil {
			return e
		}
		if _, e := w.WriteString(key); e != nil {
			return e
		}
		n = binary.PutUvarint(tmp[:], count)
		_, e := w.Write(tmp[:n])
		return e
	}
	if err := produce(emit); err != nil {
		f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// reader streams records out of one run file.
type reader struct {
	f     *os.File
	r     *bufio.Reader
	cur   Record
	done  bool
	keyFn func(string) string // optional: precompute cur.Sort per record
}

// openReader opens path with a bufSize read buffer and reads the first record.
// When keyFn is non-nil each record's Sort field is set from it.
func openReader(path string, bufSize int, keyFn func(string) string) (*reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rr := &reader{f: f, r: bufio.NewReaderSize(f, bufSize), keyFn: keyFn}
	if err := rr.advance(); err != nil {
		f.Close()
		return nil, err
	}
	return rr, nil
}

// advance decodes the next record into cur, or sets done at EOF.
func (rr *reader) advance() error {
	kl, err := binary.ReadUvarint(rr.r)
	if err != nil {
		if err == io.EOF {
			rr.done = true
			return nil
		}
		return err
	}
	key := make([]byte, kl)
	if _, err := io.ReadFull(rr.r, key); err != nil {
		return err
	}
	count, err := binary.ReadUvarint(rr.r)
	if err != nil {
		return err
	}
	rr.cur = Record{Key: string(key), Count: count}
	if rr.keyFn != nil {
		rr.cur.Sort = rr.keyFn(rr.cur.Key)
	}
	return nil
}

// close releases the run file (read-only temporary; close error ignored).
func (rr *reader) close() { _ = rr.f.Close() }

// mergeHeap orders reader indices by less applied to their current records; it
// implements heap.Interface to form the k-way merge frontier.
type mergeHeap struct {
	readers []*reader
	idx     []int
	less    func(a, b Record) bool
}

func (h *mergeHeap) Len() int { return len(h.idx) }
func (h *mergeHeap) Less(i, j int) bool {
	return h.less(h.readers[h.idx[i]].cur, h.readers[h.idx[j]].cur)
}
func (h *mergeHeap) Swap(i, j int) { h.idx[i], h.idx[j] = h.idx[j], h.idx[i] }
func (h *mergeHeap) Push(x any)    { h.idx = append(h.idx, x.(int)) }
func (h *mergeHeap) Pop() any {
	old := h.idx
	n := len(old)
	v := old[n-1]
	h.idx = old[:n-1]
	return v
}

// Merge performs a k-way merge of the given run files into a single ordered
// stream. When the run count exceeds maxFanIn it cascades through multiple
// passes — merging groups into intermediate runs (written to tmpDir and removed
// as they are consumed) — so each pass opens at most maxFanIn files and per-run
// read buffers stay large. Equal keys remain globally adjacent across passes,
// so a caller that sums adjacent equal keys still gets exact totals.
//
// Args:
//
//	paths:      run files, each already ordered by less.
//	readBudget: total read-buffer ceiling in bytes for a pass, divided across
//	            that pass's runs and clamped per run (minReadBuf..maxReadBuf).
//	tmpDir:     directory for intermediate runs during cascaded passes.
//	keyFn:      optional; when non-nil, each record's Sort field is set from it
//	            once at read time so less can compare on it cheaply (may be nil).
//	less:       the per-record ordering (same one the runs were written with).
//	emit:       called for every record in fully-merged order.
//
// Returns:
//
//	the first open/decode/emit/spill error; nil on success.
func Merge(paths []string, readBudget int64, tmpDir string, keyFn func(string) string, less func(a, b Record) bool, emit func(key string, count uint64) error) error {
	pass := 0
	for len(paths) > maxFanIn { // bounded: each pass divides the count by maxFanIn
		var next []string
		for i := 0; i < len(paths); i += maxFanIn {
			end := i + maxFanIn
			if end > len(paths) {
				end = len(paths)
			}
			out := filepath.Join(tmpDir, fmt.Sprintf("merge-%d-%05d.run", pass, len(next)))
			if err := mergeToFile(paths[i:end], readBudget, out, keyFn, less); err != nil {
				return err
			}
			next = append(next, out)
		}
		if pass > 0 { // reclaim the intermediates we created in the previous pass
			for _, p := range paths {
				os.Remove(p)
			}
		}
		paths = next
		pass++
	}
	err := mergeStream(paths, readBudget, keyFn, less, emit)
	if pass > 0 { // reclaim the final-level intermediates
		for _, p := range paths {
			os.Remove(p)
		}
	}
	return err
}

// mergeToFile merges paths into a single ordered run written to out.
func mergeToFile(paths []string, readBudget int64, out string, keyFn func(string) string, less func(a, b Record) bool) error {
	return Write(out, func(emit func(key string, count uint64) error) error {
		return mergeStream(paths, readBudget, keyFn, less, emit)
	})
}

// mergeStream merges up to maxFanIn ordered runs in a single pass, calling emit
// for each record in merged order. Read buffers total readBudget, split per run.
func mergeStream(paths []string, readBudget int64, keyFn func(string) string, less func(a, b Record) bool, emit func(key string, count uint64) error) error {
	bufSize := minReadBuf
	if len(paths) > 0 {
		per := readBudget / int64(len(paths))
		if per > int64(bufSize) {
			bufSize = int(per)
		}
	}
	if bufSize > maxReadBuf {
		bufSize = maxReadBuf
	}

	h := &mergeHeap{less: less}
	for _, p := range paths { // bounded by len(paths) (rule 2)
		rr, err := openReader(p, bufSize, keyFn)
		if err != nil {
			for _, o := range h.readers {
				o.close()
			}
			return err
		}
		h.readers = append(h.readers, rr)
	}
	defer func() {
		for _, rr := range h.readers {
			rr.close()
		}
	}()

	for i, rr := range h.readers {
		if !rr.done {
			h.idx = append(h.idx, i)
		}
	}
	heap.Init(h)

	for h.Len() > 0 { // bounded: each iteration consumes one record
		i := h.idx[0]
		rr := h.readers[i]
		rec := rr.cur
		if err := emit(rec.Key, rec.Count); err != nil {
			return err
		}
		if err := rr.advance(); err != nil {
			return err
		}
		if rr.done {
			heap.Pop(h)
		} else {
			heap.Fix(h, 0)
		}
	}
	return nil
}
