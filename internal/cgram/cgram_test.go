package cgram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, content string, norm bool, minLength int, purist bool, subLen int) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := Generate(p, 1<<20, nil, norm, minLength, purist, subLen, func(s string) error {
		got = append(got, s)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

// In purist mode "Ab1!cd" splits into segments: "A","b","1","!","cd".
func TestMinLengthFilter(t *testing.T) {
	const line = "Ab1!cd\n"

	all := run(t, line, false, 1, true, 0)
	if strings.Join(all, "|") != "A|b|1|!|cd" {
		t.Fatalf("minLength=1 got %v", all)
	}

	// minLength=2 drops every single-rune segment, keeping only "cd".
	two := run(t, line, false, 2, true, 0)
	if strings.Join(two, "|") != "cd" {
		t.Errorf("minLength=2 got %v, want [cd]", two)
	}
}

// A value < 1 is treated as 1 (keep everything, never emit empties).
func TestMinLengthClamped(t *testing.T) {
	got := run(t, "a-b\n", false, 0, true, 0)
	if strings.Join(got, "|") != "a|-|b" {
		t.Errorf("got %v, want [a - b]", got)
	}
}

// Multibyte segments are measured in runes, not bytes (purist mode).
func TestMinLengthRuneCounted(t *testing.T) {
	// "éé" is one lowercase-letter segment of 2 runes (4 bytes).
	got := run(t, "éé9\n", false, 2, true, 0)
	if strings.Join(got, "|") != "éé" {
		t.Errorf("got %v, want [éé] (2 runes kept, single digit dropped)", got)
	}
}

// Default mode: a short MIDDLE run is an insertion and glues; runs at the
// start/end always split. subLen tunes the insertion threshold.
func TestInsertionGluing(t *testing.T) {
	cases := []struct {
		in      string
		subLen  int
		want    string
	}{
		{"A1two", 1, "A1two"},
		{"1two", 1, "1|two"},
		{"two1", 1, "two|1"},
		{"ab1cd2ef", 1, "ab1cd2ef"},
		{"ab12cd", 1, "ab|12|cd"},
		{"ab12cd", 2, "ab12cd"},
		{"p4ssw0rd", 1, "p4ssw0rd"},   // glued without --normalize
		{"ab1!cd", 1, "ab1!cd"},       // consecutive insertions glue through
		{"12a34", 1, "12a34"},         // a letter inserted among digits glues too
	}
	for _, c := range cases {
		got := run(t, c.in+"\n", false, 1, false, c.subLen)
		if strings.Join(got, "|") != c.want {
			t.Errorf("%q subLen=%d: got %v, want %q", c.in, c.subLen, got, c.want)
		}
	}
}

// Default mode stores BOTH the whole glued gram and any camelCase sub-words of
// >= caseSplitMin runes; if no token qualifies only the whole is kept.
func TestCamelCaseDecomposition(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"HelloWorld", "HelloWorld|Hello|World"},
		{"HelLoWoRlD", "HelLoWoRlD"},
		{"Pass1Word", "Pass1Word|Pass|Word"},
		{"McDonald", "McDonald|Donald"},
		{"Hello", "Hello"}, // single token equals the whole: emitted once, no dup
	}
	for _, c := range cases {
		got := run(t, c.in+"\n", false, 1, false, 1)
		if strings.Join(got, "|") != c.want {
			t.Errorf("%q: got %v, want %q", c.in, got, c.want)
		}
	}
}

// Case folding: default mode keeps mixed-case letters together; purist mode
// splits on every case change.
func TestCaseFolding(t *testing.T) {
	mixed := run(t, "AbCdEf\n", false, 1, false, 1)
	if strings.Join(mixed, "|") != "AbCdEf" {
		t.Errorf("mixed got %v, want [AbCdEf]", mixed)
	}
	pur := run(t, "AbCdEf\n", false, 1, true, 0)
	if strings.Join(pur, "|") != "A|b|C|d|E|f" {
		t.Errorf("purist got %v, want [A b C d E f]", pur)
	}
}

// Letters of any script fold to one class, so a mixed Latin+Cyrillic word is a
// single segment in default mode.
func TestCrossScriptFolding(t *testing.T) {
	got := run(t, "AbДе\n", false, 1, false, 1)
	if strings.Join(got, "|") != "AbДе" {
		t.Errorf("got %v, want [AbДе] (one folded letter segment)", got)
	}
}

// --normalize composes with the default path: de-leeting collapses the line to
// a single letter segment before splitting.
func TestNormalizeWithGluing(t *testing.T) {
	got := run(t, "p4ssw0rd\n", true, 1, false, 1)
	if strings.Join(got, "|") != "password" {
		t.Errorf("got %v, want [password]", got)
	}
}

// runTemplates collects each line's parts (copied, since the slice is reused).
func runTemplates(t *testing.T, content string, norm, purist bool, subLen int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var lines []string
	if err := GenerateTemplates(p, 1<<20, nil, norm, purist, subLen, func(line string, parts []string) error {
		// parts must concatenate back to the line (contiguous partition).
		if strings.Join(parts, "") != line {
			t.Errorf("parts %v do not reconstruct line %q", parts, line)
		}
		lines = append(lines, strings.Join(parts, "/"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "|")
}

// GenerateTemplates yields whole segments per line, never length-filtered or
// camelCase-decomposed, so the parts reconstruct the line in order.
func TestGenerateTemplates(t *testing.T) {
	got := runTemplates(t, "P0tato123!\nhello\nA\n", false, false, 1)
	if got != "P0tato/123/!|hello|A" {
		t.Errorf("default got %q", got)
	}
	// purist splits every class change (no folding/gluing).
	if pur := runTemplates(t, "P0tato\n", false, true, 0); pur != "P/0/tato" {
		t.Errorf("purist got %q, want P/0/tato", pur)
	}
	// subLen widens what counts as a middle insertion.
	if g := runTemplates(t, "ab12cd\n", false, false, 2); g != "ab12cd" {
		t.Errorf("subLen=2 got %q, want ab12cd", g)
	}
}
