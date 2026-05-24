// Package progress renders a TQDM-style single-line progress bar to a writer
// (typically stderr). Redraws are throttled so frequent updates stay cheap, and
// the bar overwrites itself in place using a carriage return.
package progress

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Vavaldi/gramify/internal/humanize"
)

// redrawInterval throttles how often the bar repaints.
const redrawInterval = 100 * time.Millisecond

// barWidth is the number of cells in the rendered bar.
const barWidth = 30

// partials are the 1/8-cell leading-edge glyphs (index 0 = empty .. 8 = full).
var partials = []rune(" ▏▎▍▌▋▊▉█")

// Bar is a self-overwriting progress bar. It is not safe for concurrent use;
// drive it from a single goroutine.
type Bar struct {
	w        io.Writer
	label    string
	total    int64
	cur      int64
	asCount  bool // format values as plain counts rather than byte sizes
	start    time.Time
	lastDraw time.Time
}

// New creates a Bar writing to w. total is the expected final count (e.g. input
// file size in bytes); a non-positive total renders an indeterminate
// count-only bar. label prefixes the line.
func New(w io.Writer, total int64, label string) *Bar {
	return &Bar{w: w, label: label, total: total, start: time.Now()}
}

// NewCounter creates an indeterminate Bar that reports a running count (with no
// known total), formatting values as plain counts (e.g. 1.2M) rather than byte
// sizes. Suitable for a phase whose final size is not known up front; call
// SetTotal once it becomes known to switch to a percentage bar.
func NewCounter(w io.Writer, label string) *Bar {
	return &Bar{w: w, label: label, asCount: true, start: time.Now()}
}

// SetTotal sets the expected final count, switching an indeterminate bar to a
// percentage bar (keeping its value formatting). Used once the work size is
// known mid-run (e.g. the distinct-key count after the merge phase).
func (b *Bar) SetTotal(total int64) { b.total = total }

// human formats v using the bar's unit convention.
func (b *Bar) human(v float64) string {
	if b.asCount {
		return humanize.Count(v)
	}
	return humanize.CompactBytes(v)
}

// Add advances the bar by n and repaints if the throttle interval has elapsed.
func (b *Bar) Add(n int64) {
	b.cur += n
	now := time.Now()
	if now.Sub(b.lastDraw) < redrawInterval {
		return
	}
	b.lastDraw = now
	b.draw()
}

// Finish paints a final frame and moves to a new line.
func (b *Bar) Finish() {
	b.draw()
	fmt.Fprintln(b.w)
}

// draw paints one frame, overwriting the current line. Determinate bars show a
// "<elapsed><<eta>" estimate; indeterminate bars show elapsed only.
func (b *Bar) draw() {
	elapsed := time.Since(b.start)
	secs := elapsed.Seconds()
	if secs <= 0 {
		secs = 1e-9
	}
	rate := float64(b.cur) / secs

	if b.total <= 0 { // indeterminate: count + elapsed + rate only
		fmt.Fprintf(b.w, "\r%s %s [%s, %s/s]",
			b.label, b.human(float64(b.cur)), humanize.Duration(elapsed), b.human(rate))
		return
	}

	pct := float64(b.cur) / float64(b.total)
	if pct > 1 {
		pct = 1
	}
	fmt.Fprintf(b.w, "\r%s %3.0f%%|%s| %s/%s [%s<%s, %s/s]",
		b.label, pct*100, renderBar(pct), b.human(float64(b.cur)),
		b.human(float64(b.total)), humanize.Duration(elapsed), b.eta(rate), b.human(rate))
}

// eta estimates the time remaining at the current rate. It returns "??:??" when
// the rate is not yet meaningful.
func (b *Bar) eta(rate float64) string {
	remaining := b.total - b.cur
	if remaining <= 0 {
		return humanize.Duration(0)
	}
	if rate <= 0 {
		return "??:??"
	}
	return humanize.Duration(time.Duration(float64(remaining) / rate * float64(time.Second)))
}

// renderBar returns a barWidth-cell bar filled to fraction pct (0..1), using a
// fractional leading-edge glyph for smoothness.
func renderBar(pct float64) string {
	exact := pct * barWidth
	full := int(exact)
	var sb strings.Builder
	sb.Grow(barWidth * 3)
	for i := 0; i < barWidth; i++ { // bounded by barWidth (rule 2)
		switch {
		case i < full:
			sb.WriteRune('█')
		case i == full:
			lvl := int((exact - float64(full)) * 8)
			sb.WriteRune(partials[lvl])
		default:
			sb.WriteRune(' ')
		}
	}
	return sb.String()
}
