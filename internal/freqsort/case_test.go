package freqsort

import (
	"strings"
	"testing"
)

// collect runs a Sorter over keys (each added once per occurrence) and returns
// the weighted output lines ("<count>\t<key>").
func collect(t *testing.T, memBudget int64, caseInsensitive bool, keys []string) []string {
	t.Helper()
	var out strings.Builder
	s, err := New(&out, true /*weighted*/, 1, memBudget, t.TempDir(), caseInsensitive)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := s.Add(k); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
}

func occurrences(m map[string]int) []string {
	var keys []string
	for k, n := range m {
		for i := 0; i < n; i++ {
			keys = append(keys, k)
		}
	}
	return keys
}

// Case-insensitive grouping sums case variants and emits the most-frequent
// casing with the total (ties pick the lexicographically smaller variant).
func TestCaseInsensitiveMostFrequent(t *testing.T) {
	in := occurrences(map[string]int{"Potato": 5, "potato": 3, "POTATO": 1, "Apple": 2, "apple": 2})
	// Run both in-memory (large budget) and spilled (tiny budget) to exercise
	// the single-run and multi-run merge paths.
	for _, budget := range []int64{1 << 20, 1} {
		got := collect(t, budget, true, in)
		want := []string{"9\tPotato", "4\tApple"} // Potato wins 9; Apple/apple tie -> Apple
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("budget=%d got %v, want %v", budget, got, want)
		}
	}
}

// With case sensitivity (the default for non-charset modes) variants stay
// distinct, reproducing the original behavior.
func TestCaseSensitiveKeepsVariants(t *testing.T) {
	in := occurrences(map[string]int{"Potato": 5, "potato": 3})
	got := collect(t, 1<<20, false, in)
	want := []string{"5\tPotato", "3\tpotato"} // count desc
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v, want %v", got, want)
	}
}
