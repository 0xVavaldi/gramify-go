package progress

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderBarWidth(t *testing.T) {
	for _, pct := range []float64{0, 0.25, 0.5, 0.999, 1} {
		got := utf8.RuneCountInString(renderBar(pct))
		if got != barWidth {
			t.Errorf("renderBar(%v) width = %d runes, want %d", pct, got, barWidth)
		}
	}
}

func TestCounterIsIndeterminate(t *testing.T) {
	var buf bytes.Buffer
	b := NewCounter(&buf, "sorting")
	b.Add(1500)
	b.Finish()
	out := buf.String()
	if strings.Contains(out, "%") {
		t.Errorf("counter should show no percentage: %q", out)
	}
	if !strings.Contains(out, "1.5k") {
		t.Errorf("counter should show plain count 1.5k: %q", out)
	}
}

func TestFinishShowsComplete(t *testing.T) {
	var buf bytes.Buffer
	b := New(&buf, 1000, "processing")
	b.Add(1000)
	b.Finish()
	out := buf.String()
	if !strings.Contains(out, "100%") {
		t.Errorf("output missing 100%%: %q", out)
	}
	if !strings.Contains(out, "1000B/1000B") {
		t.Errorf("output missing size: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("Finish should end with newline")
	}
}

// TestSetTotalShowsPercent: an indeterminate counter switches to a percentage
// bar (with ETA) once SetTotal is called.
func TestSetTotalShowsPercent(t *testing.T) {
	var buf bytes.Buffer
	b := NewCounter(&buf, "sorting")
	b.SetTotal(1000)
	b.Add(1000)
	b.Finish()
	out := buf.String()
	if !strings.Contains(out, "100%") {
		t.Errorf("expected percentage after SetTotal: %q", out)
	}
	if !strings.Contains(out, "<") {
		t.Errorf("expected ETA separator '<': %q", out)
	}
}
