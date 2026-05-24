// Package dedup provides the --stdout output writer, in two modes.
//
// Cache disabled (--disable-cache): a pure streaming passthrough. Every key is
// written immediately in arrival order, duplicates included, with no memory
// constraint.
//
// Cache enabled (default): an accurate, memory-bounded de-duplicator. Keys and
// their exact occurrence counts are held in a byte-bounded map. When the map
// exceeds its budget it is partially evicted — the coldest entries (lowest
// aging weight) are written to disk as a key-sorted (key,count) run and dropped,
// while surviving entries keep their *exact* count and only have their aging
// weight halved. Crucially the true count (value) and the eviction weight are
// tracked separately, so aging never corrupts a count. On Finalize the
// remaining in-memory entries and every spilled run are k-way merged by key
// (summing equal keys), and each distinct key is emitted exactly once, in key
// order.
//
// Because everything overflowing the budget goes to disk and is reconciled at
// the end, the result is exact regardless of how the input size compares to
// RAM: a smaller budget simply spills more often. This is what lets a 64 MiB
// budget de-duplicate a multi-gigabyte stream. The merge phase's read buffers
// are themselves budget-bounded (see runfile.Merge).
//
// Counts are not printed (stdout output is bare keys; --weighted/--min-freq are
// file-only), but are tracked exactly so the merge stays correct.
package dedup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Vavaldi/gramify/internal/membudget"
	"github.com/Vavaldi/gramify/internal/runfile"
)

// entryOverhead approximates the non-key cost of one map entry (the entry
// struct plus Go map bucket/overflow overhead). Conservative, so the live map
// stays under the byte budget.
const entryOverhead = 48

// maxWeight caps the aging weight to avoid overflow on extremely hot keys.
const maxWeight = 1 << 30

// entry holds a key's exact count and a separate aging weight.
type entry struct {
	value  uint64 // exact occurrence count; never aged
	weight uint32 // eviction score; halved on each sweep
}

// Writer streams keys to an io.Writer. With caching enabled it produces an
// exact, key-ordered, de-duplicated result via disk-backed external merge.
type Writer struct {
	w     *bufio.Writer
	cache bool

	memBudget int64
	buf       map[string]entry
	bytes     int64
	tmpDir    string
	runs      []string
}

// New wraps out as a Writer.
//
// Args:
//
//	out:      destination; bare grams are written here. Must be non-nil.
//	cache:    when true, output is de-duplicated exactly via a byte-bounded map
//	          that spills to disk; when false, keys stream through unfiltered.
//	budget:   in-memory ceiling in bytes before spilling (<= 0 uses
//	          DefaultMemBudget). Ignored when cache is false.
//	cacheDir: parent directory for spill files ("" = system temp dir).
//
// Returns:
//
//	the Writer, or an error if the temp spill directory cannot be created.
//	Panics if out is nil (programmer-contract violation; NASA P10 rule 5).
func New(out io.Writer, cache bool, budget int64, cacheDir string) (*Writer, error) {
	if out == nil {
		panic("dedup.New: nil writer")
	}
	d := &Writer{w: bufio.NewWriterSize(out, 1<<20), cache: cache}
	if cache {
		if budget <= 0 {
			budget = membudget.DefaultMemBudget
		}
		d.memBudget = budget
		d.buf = make(map[string]entry)
		dir, err := os.MkdirTemp(cacheDir, "gramify-dedup-")
		if err != nil {
			return nil, err
		}
		d.tmpDir = dir
	}
	return d, nil
}

// Add records one occurrence of key. In passthrough mode (cache disabled) it is
// written immediately. In cache mode it bumps the key's exact count and aging
// weight and, if the byte budget is now exceeded, spills the coldest entries to
// disk. Nothing is emitted in cache mode until Finalize.
//
// Returns:
//
//	the first write or spill error; nil on success.
func (d *Writer) Add(key string) error {
	if !d.cache {
		return d.emit(key)
	}
	e, ok := d.buf[key]
	if !ok {
		d.bytes += int64(len(key)) + entryOverhead
	}
	e.value++
	if e.weight < maxWeight {
		e.weight++
	}
	d.buf[key] = e
	if d.bytes >= d.memBudget {
		return d.spillCold()
	}
	return nil
}

// spillCold evicts the coldest entries (aging weight <= 1) to a key-sorted run
// and ages the survivors (weight halved, value untouched). If every entry is
// hot, the whole buffer is spilled to guarantee forward progress.
func (d *Writer) spillCold() error {
	victims := make([]string, 0, len(d.buf))
	for k, e := range d.buf { // bounded by map size (rule 2)
		if e.weight <= 1 {
			victims = append(victims, k)
		}
	}
	evictAll := len(victims) == 0
	if evictAll {
		for k := range d.buf {
			victims = append(victims, k)
		}
	}
	if err := d.writeRun(victims); err != nil {
		return err
	}
	for _, k := range victims { // bounded by len(victims) (rule 2)
		d.bytes -= int64(len(k)) + entryOverhead
		delete(d.buf, k)
	}
	if !evictAll {
		for k, e := range d.buf { // age survivors (rule 2)
			e.weight >>= 1
			d.buf[k] = e
		}
	}
	return nil
}

// writeRun writes the given keys (with their exact counts) as a key-sorted run
// file. keys is sorted in place.
func (d *Writer) writeRun(keys []string) error {
	sort.Strings(keys)
	path := filepath.Join(d.tmpDir, fmt.Sprintf("d%05d.run", len(d.runs)))
	if err := runfile.Write(path, func(emit func(string, uint64) error) error {
		for _, k := range keys { // bounded by len(keys) (rule 2)
			if err := emit(k, d.buf[k].value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	d.runs = append(d.runs, path)
	return nil
}

// Finalize flushes output and releases temporary files. In passthrough mode it
// just flushes. In cache mode it produces the exact de-duplicated result: if
// nothing spilled it sorts the in-memory keys and emits them; otherwise it
// spills the remainder and k-way merges every run by key, emitting each
// distinct key once. The Writer must not be used afterwards.
//
// Returns:
//
//	the first write/merge error; nil on success.
func (d *Writer) Finalize() error {
	if !d.cache {
		return d.w.Flush()
	}
	defer d.Close()

	// Fast path: nothing spilled, so the whole distinct set is in memory.
	if len(d.runs) == 0 {
		keys := make([]string, 0, len(d.buf))
		for k := range d.buf {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys { // bounded by len(keys) (rule 2)
			if err := d.emit(k); err != nil {
				return err
			}
		}
		return d.w.Flush()
	}

	// Spill the remainder, then k-way merge every run by key and emit each
	// distinct key exactly once (equal keys collapse).
	if len(d.buf) > 0 {
		keys := make([]string, 0, len(d.buf))
		for k := range d.buf {
			keys = append(keys, k)
		}
		if err := d.writeRun(keys); err != nil {
			return err
		}
		d.buf = nil
		d.bytes = 0
	}
	var curKey string
	have := false
	err := runfile.Merge(d.runs, d.memBudget, d.tmpDir, nil,
		func(a, b runfile.Record) bool { return a.Key < b.Key },
		func(k string, _ uint64) error {
			if have && k == curKey {
				return nil // duplicate of the key just emitted
			}
			if err := d.emit(k); err != nil {
				return err
			}
			curKey, have = k, true
			return nil
		})
	if err != nil {
		return err
	}
	return d.w.Flush()
}

// Close releases temporary files. Safe to call on the abort path or multiple
// times; a no-op in passthrough mode.
func (d *Writer) Close() {
	if d.tmpDir != "" {
		os.RemoveAll(d.tmpDir)
		d.tmpDir = ""
	}
}

// emit writes one key followed by a newline.
func (d *Writer) emit(key string) error {
	if _, err := d.w.WriteString(key); err != nil {
		return err
	}
	return d.w.WriteByte('\n')
}
