// Package freqsort implements an external (disk-backed) frequency sorter.
//
// Grams are streamed in via Add. Memory is bounded: at most maxEntries
// distinct keys are held before being spilled to a sorted run file. The
// pipeline is:
//
//	Phase A  count distinct keys with a bounded map, spilling key-sorted runs.
//	Phase B  k-way merge the key-sorted runs, summing counts -> unique (key,count).
//	Phase C  external sort those records by count desc, key asc -> output.
//
// Default output is one gram per line, most frequent first. With Weighted set,
// each line is "<count>\t<gram>" (like `uniq -c`, tab-separated so grams that
// contain spaces stay parseable).
//
// The full dataset is never held in memory; only bounded buffers plus
// temporary run files on disk are used.
package freqsort

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Vavaldi/gramify/internal/membudget"
	"github.com/Vavaldi/gramify/internal/runfile"
)

// mapEntryOverhead approximates the non-key cost of one map entry (count value
// plus Go map bucket/overflow overhead); recordOverhead does the same for a
// buffered (key,count) record. Conservative, so the live buffer stays under
// the byte budget.
const (
	mapEntryOverhead = 48
	recordOverhead   = 40
)

// Sorter accumulates keys and, on Finalize, writes them to out ordered by
// descending frequency.
type Sorter struct {
	out       *bufio.Writer
	weighted  bool
	memBudget int64  // spill the buffer once it reaches this many bytes
	minCount  uint64 // keys occurring fewer than this many times are dropped

	// caseInsensitive groups keys by their lowercase form: counts of all case
	// variants are summed and the most-frequent original casing is emitted with
	// that total (ties broken by the lexicographically smallest variant).
	caseInsensitive bool

	cacheDir string // parent for spill files; tmpDir is created under it lazily
	tmpDir   string // created on first spill (empty until then), removed by Close

	pool *Pool // optional shared memory budget across many Sorters (nil = solo)

	buf     map[string]uint64
	bytes   int64    // approximate live size of buf
	keyRuns []string // phase A spill files (key-sorted)

	progress Progress // optional: drives a "sorting" bar during Finalize
}

// Progress receives sort-phase progress: SetTotal is called once with the final
// record count (known after the merge phase), then Add is called per emitted
// record. *progress.Bar satisfies it.
type Progress interface {
	SetTotal(total int64)
	Add(n int64)
}

// SetProgress registers a Progress to drive a "sorting" bar during Finalize.
// Pass nil to disable.
func (s *Sorter) SetProgress(p Progress) { s.progress = p }

// New creates a Sorter writing to out.
//
// Args:
//
//	out:       destination for the sorted result. Must be non-nil; the caller
//	           closes its underlying destination after Finalize.
//	weighted:  when true, prefix each line with its count ("<count>\t<gram>").
//	minCount:  drop keys occurring fewer than this many times (< 1 means 1, no
//	           filtering).
//	memBudget: bytes held in memory before a run is spilled (<= 0 uses
//	           DefaultMemBudget).
//	cacheDir:  parent directory for spill files ("" = system temp dir).
//	caseInsensitive: group case variants together (see the Sorter field).
//
// Returns:
//
//	the Sorter, or an error if out is nil. The temp spill directory is created
//	lazily on the first spill, so a Sorter whose data fits in memory never
//	touches the filesystem for temporaries.
func New(out io.Writer, weighted bool, minCount uint64, memBudget int64, cacheDir string, caseInsensitive bool) (*Sorter, error) {
	// precondition guard (NASA P10 rule 7: validate parameters)
	if out == nil {
		return nil, fmt.Errorf("freqsort.New: nil writer")
	}
	if minCount < 1 {
		minCount = 1
	}
	if memBudget <= 0 {
		memBudget = membudget.DefaultMemBudget
	}
	return &Sorter{
		out:             bufio.NewWriterSize(out, 1<<20),
		weighted:        weighted,
		memBudget:       memBudget,
		minCount:        minCount,
		caseInsensitive: caseInsensitive,
		cacheDir:        cacheDir,
		buf:             make(map[string]uint64),
	}, nil
}

// ensureTmpDir returns the Sorter's temp directory, creating it under cacheDir
// on first use. Sinks that never spill never call this, so they create no
// directory at all.
func (s *Sorter) ensureTmpDir() (string, error) {
	if s.tmpDir == "" {
		dir, err := os.MkdirTemp(s.cacheDir, "gramify-freqsort-")
		if err != nil {
			return "", err
		}
		s.tmpDir = dir
	}
	return s.tmpDir, nil
}

// Pool bounds the combined in-memory buffer bytes of a set of Sorters that fill
// concurrently (e.g. the many per-bucket sinks of charset template mode). When
// the shared total reaches the limit the largest member spills, so an idle or
// tiny Sorter lends its share to a busy one and a single large Sorter can use
// almost the whole budget instead of a fixed 1/N slice. This both caps total
// memory and minimises spills — and thus the sort/merge work they create.
//
// A Pool is not safe for concurrent use; all its Sorters must be driven from a
// single goroutine (as template mode does).
type Pool struct {
	limit int64
	used  int64
	sinks []*Sorter
}

// NewPool returns a Pool with the given combined byte limit.
func NewPool(limit int64) *Pool {
	if limit < 1 {
		limit = 1
	}
	return &Pool{limit: limit}
}

// UsePool attaches s to p so its buffer counts against the shared limit. Call
// once, right after New and before any Add (no-op when p is nil).
func (s *Sorter) UsePool(p *Pool) {
	s.pool = p
	if p != nil {
		p.sinks = append(p.sinks, s)
	}
}

// spillLargest spills members until the pool is back under its limit, always
// picking the current largest buffer so each spill frees as much as possible
// (avoiding a thrash of tiny spills). It is a no-op while under the limit.
func (p *Pool) spillLargest() error {
	for p.used >= p.limit { // bounded: each pass spills a non-empty sink (rule 2)
		var big *Sorter
		for _, s := range p.sinks {
			if big == nil || s.bytes > big.bytes {
				big = s
			}
		}
		if big == nil || big.bytes == 0 {
			return nil // nothing left to reclaim
		}
		if err := big.spillKeyRun(); err != nil {
			return err
		}
	}
	return nil
}

// fold maps a key to its grouping key: the lowercase form when case-insensitive,
// otherwise the key itself (the identity fast path, no allocation).
func (s *Sorter) fold(k string) string {
	if s.caseInsensitive {
		return strings.ToLower(k)
	}
	return k
}

// keyLess orders run records for the Phase B merge: by fold key first (so case
// variants are adjacent), then by exact key (so identical variants merge and
// ties pick the smallest casing). With case-sensitivity off this reduces to a
// plain key comparison.
//
// Records carry a precomputed fold in Record.Sort (populated by runfile.Merge),
// so this does no per-comparison strings.ToLower.
func (s *Sorter) keyLess(a, b runfile.Record) bool {
	if a.Sort != b.Sort {
		return a.Sort < b.Sort
	}
	return a.Key < b.Key
}

// sortKeys orders keys for a Phase A spill by (fold, exact key). In
// case-insensitive mode it precomputes each key's lowercase form once (O(n))
// instead of letting the comparator call strings.ToLower on every comparison
// (O(n log n)) — that comparison cost otherwise dominates CPU on large inputs.
func (s *Sorter) sortKeys(keys []string) {
	if !s.caseInsensitive {
		sort.Strings(keys)
		return
	}
	type kf struct{ key, fold string }
	tmp := make([]kf, len(keys))
	for i, k := range keys {
		tmp[i] = kf{key: k, fold: strings.ToLower(k)}
	}
	sort.Slice(tmp, func(i, j int) bool {
		if tmp[i].fold != tmp[j].fold {
			return tmp[i].fold < tmp[j].fold
		}
		return tmp[i].key < tmp[j].key
	})
	for i := range tmp {
		keys[i] = tmp[i].key
	}
}

// Add records one occurrence of key, spilling a key-sorted run to disk when the
// in-memory buffer (or, when pooled, the shared pool) reaches its byte budget.
//
// Returns:
//
//	the error from a triggered spill; nil otherwise.
func (s *Sorter) Add(key string) error {
	if _, ok := s.buf[key]; !ok {
		sz := int64(len(key)) + mapEntryOverhead
		s.bytes += sz
		if s.pool != nil {
			s.pool.used += sz
		}
	}
	s.buf[key]++
	if s.pool != nil {
		// Per-sink cap keeps any one buffer (hence run/sort/GC-scan) bounded;
		// the pool then caps the combined total, spilling the largest member.
		if s.bytes >= s.memBudget {
			return s.spillKeyRun()
		}
		return s.pool.spillLargest()
	}
	if s.bytes >= s.memBudget {
		return s.spillKeyRun()
	}
	return nil
}

// Close removes temporary files. Safe to defer immediately after New; also
// called by Finalize on success.
func (s *Sorter) Close() {
	if s.tmpDir != "" {
		os.RemoveAll(s.tmpDir)
		s.tmpDir = ""
	}
}

// spillKeyRun flushes the in-memory count buffer to a new key-sorted run file
// and resets the buffer (Phase A).
//
// Returns:
//
//	an error if the run file cannot be written; nil on success.
func (s *Sorter) spillKeyRun() error {
	keys := make([]string, 0, len(s.buf))
	for k := range s.buf {
		keys = append(keys, k)
	}
	s.sortKeys(keys)

	dir, err := s.ensureTmpDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("k%04d.run", len(s.keyRuns)))
	if err := runfile.Write(path, func(emit func(string, uint64) error) error {
		for _, k := range keys {
			if err := emit(k, s.buf[k]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.keyRuns = append(s.keyRuns, path)
	if s.pool != nil {
		s.pool.used -= s.bytes // return this sink's bytes to the shared budget
	}
	clear(s.buf) // reuse the map's buckets; avoids discarding/regrowing them
	s.bytes = 0
	return nil
}

// grouper folds a (fold-key, exact-key)-ordered stream of (key,count) records
// into one record per fold group: the summed total emitted with the
// highest-count variant's casing (ties keep the smaller key), dropping groups
// below min. Both the in-memory fast path and the Phase B merge feed it in
// sorted order, so they group identically.
type grouper struct {
	fold func(string) string
	min  uint64
	emit func(key string, count uint64) error

	have        bool
	curFold     string // fold key of the open group
	curKey      string // the open exact variant
	curKeyCount uint64 // accumulated count of the open variant
	total       uint64 // summed count across the group's variants
	bestKey     string
	bestCount   uint64
}

// closeVariant rolls the open variant into the group total and best-of.
func (g *grouper) closeVariant() {
	g.total += g.curKeyCount
	if g.curKeyCount > g.bestCount { // strict: ties keep the smaller (earlier) key
		g.bestKey, g.bestCount = g.curKey, g.curKeyCount
	}
}

// flush emits the open group's best variant with its total (if it clears min).
func (g *grouper) flush() error {
	if !g.have {
		return nil
	}
	g.closeVariant()
	if g.total < g.min {
		return nil
	}
	return g.emit(g.bestKey, g.total)
}

// push consumes one record; records must arrive in (fold, key) order.
func (g *grouper) push(k string, c uint64) error {
	switch {
	case g.have && k == g.curKey: // same exact variant (e.g. from another run)
		g.curKeyCount += c
	case g.have && g.fold(k) == g.curFold: // same group, next variant
		g.closeVariant()
		g.curKey, g.curKeyCount = k, c
	default: // new group (or first record)
		if err := g.flush(); err != nil {
			return err
		}
		g.curFold, g.curKey, g.curKeyCount = g.fold(k), k, c
		g.total, g.bestKey, g.bestCount = 0, "", 0
		g.have = true
	}
	return nil
}

// Finalize aggregates the data and writes it to out ordered by count desc, key
// asc. When nothing spilled during Add (the common case for the many small
// sinks in template mode) the whole dataset is grouped in memory and handed to
// the count sorter — no run file is written or read back (write-once,
// read-never). Otherwise it spills the remainder and k-way merges the key runs.
// Temporary files, if any, are removed. The Sorter must not be used afterwards.
//
// Returns:
//
//	the first spill/merge/write error; nil on success.
func (s *Sorter) Finalize() error {
	defer s.Close()

	cs := &countSorter{
		out:       s.out,
		weighted:  s.weighted,
		memBudget: s.memBudget,
		tmp:       s.ensureTmpDir,
		progress:  s.progress,
	}
	g := &grouper{fold: s.fold, min: s.minCount, emit: cs.add}

	if len(s.keyRuns) == 0 {
		// Fast path: the dataset is still entirely in buf. Group it in memory and
		// hand the unique records straight to the count sorter.
		keys := make([]string, 0, len(s.buf))
		for k := range s.buf {
			keys = append(keys, k)
		}
		s.sortKeys(keys)
		for _, k := range keys { // bounded by len(buf) (rule 2)
			if err := g.push(k, s.buf[k]); err != nil {
				return err
			}
		}
	} else {
		// Slow path: spill the remaining buffer, then k-way merge all key runs.
		// Records arrive ordered by (fold, exact key) with the fold precomputed
		// in Record.Sort, so the grouper sees adjacent variants without any
		// per-comparison work.
		if len(s.buf) > 0 {
			if err := s.spillKeyRun(); err != nil {
				return err
			}
		}
		var foldFn func(string) string
		if s.caseInsensitive {
			foldFn = strings.ToLower // precomputed into Record.Sort, once per record
		}
		err := runfile.Merge(s.keyRuns, s.memBudget, s.tmpDir, foldFn, s.keyLess,
			func(k string, c uint64) error { return g.push(k, c) })
		if err != nil {
			return err
		}
	}
	if err := g.flush(); err != nil {
		return err
	}

	// Phase C: emit ordered by count desc, key asc. The distinct-record count
	// is now known, so give the progress bar a real total (percentage + ETA).
	if cs.progress != nil {
		cs.progress.SetTotal(cs.added)
	}
	if err := cs.finalize(); err != nil {
		return err
	}
	return s.out.Flush()
}

// countSorter externally sorts (key,count) records by count desc, then key asc.
type countSorter struct {
	out       *bufio.Writer
	weighted  bool
	memBudget int64
	tmp       func() (string, error) // lazily resolves the shared temp dir

	buf      []runfile.Record
	bytes    int64 // approximate live size of buf
	runs     []string
	added    int64 // total records added (the phase-C output count)
	progress Progress
}

// countLess is the final ordering: count descending, then key ascending.
//
// Args:
//
//	a, b: records to compare.
//
// Returns:
//
//	true if a should sort before b.
func countLess(a, b runfile.Record) bool {
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.Key < b.Key
}

// add buffers one (key,count) record, spilling a sorted run when the buffer
// reaches maxEntries.
//
// Args:
//
//	key:   the gram.
//	count: its aggregated occurrence count.
//
// Returns:
//
//	an error if a spill is triggered and fails; nil otherwise.
func (cs *countSorter) add(key string, count uint64) error {
	cs.buf = append(cs.buf, runfile.Record{Key: key, Count: count})
	cs.bytes += int64(len(key)) + recordOverhead
	cs.added++
	if cs.bytes >= cs.memBudget {
		return cs.spill()
	}
	return nil
}

// spill sorts the current buffer by countLess and writes it as a new run
// file, then truncates the buffer.
//
// Returns:
//
//	an error if the run file cannot be written; nil on success.
func (cs *countSorter) spill() error {
	sort.Slice(cs.buf, func(i, j int) bool { return countLess(cs.buf[i], cs.buf[j]) })
	dir, err := cs.tmp()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("c%04d.run", len(cs.runs)))
	if err := runfile.Write(path, func(emit func(string, uint64) error) error {
		for _, r := range cs.buf {
			if err := emit(r.Key, r.Count); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	cs.runs = append(cs.runs, path)
	cs.buf = cs.buf[:0]
	cs.bytes = 0
	return nil
}

// finalize emits all buffered records in final order. If nothing was ever
// spilled it sorts the buffer in memory; otherwise it spills the remainder
// and k-way merges every run.
//
// Returns:
//
//	an error from spilling, merging, or writing; nil on success.
func (cs *countSorter) finalize() error {
	if len(cs.runs) == 0 {
		sort.Slice(cs.buf, func(i, j int) bool { return countLess(cs.buf[i], cs.buf[j]) })
		for _, r := range cs.buf {
			if err := cs.write(r.Key, r.Count); err != nil {
				return err
			}
		}
		return nil
	}
	if len(cs.buf) > 0 {
		if err := cs.spill(); err != nil {
			return err
		}
	}
	dir, err := cs.tmp() // runs are non-empty here, so the dir already exists
	if err != nil {
		return err
	}
	return runfile.Merge(cs.runs, cs.memBudget, dir, nil, countLess, cs.write)
}

// write emits one result line: "<count>\t<key>" when weighted, otherwise the
// bare key.
//
// Args:
//
//	key:   the gram.
//	count: its occurrence count (only printed when weighted).
//
// Returns:
//
//	the underlying writer error, if any.
func (cs *countSorter) write(key string, count uint64) error {
	var err error
	if cs.weighted {
		_, err = fmt.Fprintf(cs.out, "%d\t%s\n", count, key)
	} else {
		_, err = cs.out.WriteString(key + "\n")
	}
	if err == nil && cs.progress != nil {
		cs.progress.Add(1)
	}
	return err
}
