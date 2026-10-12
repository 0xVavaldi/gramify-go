package runfile

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
)

// writeSortedRun writes recs (sorted by key) to a fresh run file under dir.
func writeSortedRun(t *testing.T, dir string, idx int, recs []Record) string {
	t.Helper()
	sort.Slice(recs, func(i, j int) bool { return recs[i].Key < recs[j].Key })
	p := filepath.Join(dir, fmt.Sprintf("in-%05d.run", idx))
	if err := Write(p, func(emit func(string, uint64) error) error {
		for _, r := range recs {
			if err := emit(r.Key, r.Count); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// mergeSummed merges runs by key, summing equal keys (mirrors how freqsort uses
// Merge), and returns the resulting key->total map plus the emitted key order.
func mergeSummed(t *testing.T, paths []string, budget int64, tmpDir string) (map[string]uint64, []string) {
	t.Helper()
	got := map[string]uint64{}
	var order []string
	var cur string
	var curCount uint64
	have := false
	flush := func() {
		if have {
			got[cur] += curCount
			order = append(order, cur)
		}
	}
	err := Merge(paths, budget, tmpDir, nil, func(a, b Record) bool { return a.Key < b.Key },
		func(k string, c uint64) error {
			if have && k == cur {
				curCount += c
				return nil
			}
			flush()
			cur, curCount, have = k, c, true
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	flush()
	return got, order
}

// TestMergeCascade builds far more than maxFanIn runs (forcing multi-pass
// cascading) with overlapping keys and verifies the summed result is exact and
// emitted in key order.
func TestMergeCascade(t *testing.T) {
	dir := t.TempDir()
	const runs = maxFanIn*3 + 7 // > maxFanIn^1, forces at least two passes worth
	const keysPerRun = 50
	want := map[string]uint64{}
	var paths []string
	for r := 0; r < runs; r++ {
		var recs []Record
		for k := 0; k < keysPerRun; k++ {
			key := fmt.Sprintf("key-%03d", (r*7+k)%200) // overlapping keyspace
			recs = append(recs, Record{Key: key, Count: uint64(r + 1)})
			want[key] += uint64(r + 1)
		}
		paths = append(paths, writeSortedRun(t, dir, r, recs))
	}

	// Tiny budget so per-run buffers would collapse without the fan-in cap.
	got, order := mergeSummed(t, paths, 16<<10, dir)

	if len(got) != len(want) {
		t.Fatalf("distinct keys: got %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got count %d, want %d", k, got[k], v)
		}
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("output keys not in ascending order: %v", order)
	}
}

// TestMergeSinglePass exercises the no-cascade path (runs <= maxFanIn).
func TestMergeSinglePass(t *testing.T) {
	dir := t.TempDir()
	p1 := writeSortedRun(t, dir, 0, []Record{{Key: "a", Count: 1}, {Key: "c", Count: 2}})
	p2 := writeSortedRun(t, dir, 1, []Record{{Key: "a", Count: 3}, {Key: "b", Count: 4}})
	got, order := mergeSummed(t, []string{p1, p2}, 1<<20, dir)
	want := map[string]uint64{"a": 4, "b": 4, "c": 2}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %d want %d", k, got[k], v)
		}
	}
	if fmt.Sprint(order) != "[a b c]" {
		t.Errorf("order = %v, want [a b c]", order)
	}
}
