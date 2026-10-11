package ngram

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// brute is the original gram-length-major reference: every contiguous window
// of length min..max over the flattened token stream.
func brute(tokens []string, min, max int) []string {
	var out []string
	for i := min; i <= max; i++ {
		for j := 0; j+i <= len(tokens); j++ {
			out = append(out, strings.Join(tokens[j:j+i], " "))
		}
	}
	return out
}

// writeFile lays the tokens out across lines (windows must span line breaks)
// and returns the path.
func writeFile(t *testing.T, tokens []string, perLine int) string {
	t.Helper()
	var b strings.Builder
	for i, tok := range tokens {
		b.WriteString(tok)
		if (i+1)%perLine == 0 {
			b.WriteByte('\n')
		} else {
			b.WriteByte(' ')
		}
	}
	p := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGenerateMatchesBruteForce(t *testing.T) {
	tokens := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	cases := []struct{ min, max, perLine int }{
		{1, 1, 3}, {1, 2, 3}, {2, 3, 2}, {1, 4, 1}, {3, 3, 5}, {1, 8, 4},
	}
	for _, c := range cases {
		path := writeFile(t, tokens, c.perLine)
		var got []string
		// tiny budget forces multi-block reads so windows span block boundaries
		if err := Generate(path, 1<<20, nil, c.min, c.max, func(s string) error {
			got = append(got, s)
			return nil
		}); err != nil {
			t.Fatalf("min=%d max=%d: %v", c.min, c.max, err)
		}
		want := brute(tokens, c.min, c.max)
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("min=%d max=%d perLine=%d:\n got=%v\nwant=%v", c.min, c.max, c.perLine, got, want)
		}
	}
}

func TestGenerateRejectsBadArgs(t *testing.T) {
	if err := Generate("x", 1<<20, nil, 0, 2, func(string) error { return nil }); err == nil {
		t.Error("expected error for min<1")
	}
	if err := Generate("x", 1<<20, nil, 3, 2, func(string) error { return nil }); err == nil {
		t.Error("expected error for max<min")
	}
	if err := Generate("x", 1<<20, nil, 1, 2, nil); err == nil {
		t.Error("expected error for nil emit")
	}
}
