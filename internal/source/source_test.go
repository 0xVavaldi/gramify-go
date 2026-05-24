package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reference computes the lines LinesBounded should produce: split on '\n',
// drop a single trailing empty (a final newline ends the last line rather than
// starting a new empty one), and strip a trailing '\r' from each.
func reference(content string) []string {
	if content == "" {
		return nil
	}
	parts := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimRight(p, "\r")
	}
	return parts
}

func collect(t *testing.T, content string, budget int64) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := LinesBounded(p, budget, nil, func(line string) error {
		got = append(got, line)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLinesBounded(t *testing.T) {
	long := strings.Repeat("x", 5000) // exceeds the smallest block size
	contents := []string{
		"a\nb\nc\n",
		"a\nb\nc",          // no trailing newline
		"a\r\nb\r\nc\r\n",  // CRLF
		"a\n\nb\n",         // blank middle line
		long + "\n" + long, // lines longer than a block
		"single",           // one line, no newline
		"",                 // empty file -> no lines
	}
	budgets := []int64{1 << 20, 8 << 20, 64 << 10} // exercises planBuffers clamps
	for _, content := range contents {
		want := reference(content)
		for _, b := range budgets {
			got := collect(t, content, b)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("budget=%d content=%q:\n got=%q\nwant=%q", b, content, got, want)
			}
		}
	}
}

func TestLinesBoundedCallbackError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(p, []byte(strings.Repeat("line\n", 10000)), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := os.ErrInvalid
	n := 0
	err := LinesBounded(p, 64<<10, nil, func(string) error {
		n++
		if n == 3 {
			return sentinel
		}
		return nil
	})
	if err != sentinel {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

// TestLinesBoundedProgressTotalsToSize: the onRead callback must report exactly
// the file's byte size in aggregate, so a progress bar reaches 100%.
func TestLinesBoundedProgressTotalsToSize(t *testing.T) {
	content := strings.Repeat("some line of text\n", 5000)
	p := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := LinesBounded(p, 64<<10, func(n int64) { total += n }, func(string) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if total != int64(len(content)) {
		t.Errorf("progress total = %d, want %d", total, len(content))
	}
}
