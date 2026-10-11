package dedup

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// collectLines runs adds against a Writer and returns the emitted lines.
func collectLines(t *testing.T, cache bool, budget int64, keys []string) []string {
	t.Helper()
	var buf bytes.Buffer
	w, err := New(&buf, cache, budget, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := w.Add(k); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finalize(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

// TestExactDistinctSorted: cached output is the exact distinct set, key-sorted,
// each key once. Input order is deliberately not sorted.
func TestExactDistinctSorted(t *testing.T) {
	keys := []string{"d", "b", "a", "c", "b", "a", "d", "a"}
	got := collectLines(t, true, 1<<20, keys)
	want := []string{"a", "b", "c", "d"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDisableCachePassesThrough: with caching off, every Add is emitted in
// arrival order, duplicates included.
func TestDisableCachePassesThrough(t *testing.T) {
	keys := []string{"b", "a", "b", "a", "a"}
	got := collectLines(t, false, 1<<20, keys)
	if strings.Join(got, "|") != "b|a|b|a|a" {
		t.Errorf("passthrough altered output: %v", got)
	}
}

// TestAccurateUnderTinyBudget is the small-RAM-large-input case: a tiny budget
// forces many disk spills, yet the output must still be the exact distinct set
// (no duplicates, nothing missing), key-sorted. Mixes a hot key with many cold
// ones to exercise the value/weight separation and partial eviction.
func TestAccurateUnderTinyBudget(t *testing.T) {
	const n = 20000
	var keys []string
	want := make([]string, 0, n+1)
	want = append(want, "hot")
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("cold-%06d", i)
		want = append(want, k)
		keys = append(keys, "hot", k, "hot") // hot recurs constantly
	}
	got := collectLines(t, true, 64<<10 /* 64 KiB: forces spilling */, keys)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("got %d distinct keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at %d: got %q want %q", i, got[i], want[i])
		}
	}
}
