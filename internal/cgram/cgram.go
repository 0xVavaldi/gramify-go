// Package cgram implements charset mode: split each line into segments at
// character-class boundaries and emit them.
//
// The default ("password-practice") path folds upper- and lowercase letters of
// any script into one "letter" class, treats short runs sitting in the middle
// of a line as inserted/leetspeak substitutions and glues across them, and
// additionally surfaces camelCase sub-words (storing both the whole and the
// parts). The --purist path keeps strict per-class splitting (lower, upper,
// digit, other), dropping runs shorter than minLength. A class change ends the
// current segment.
package cgram

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Vavaldi/gramify/internal/normalize"
	"github.com/Vavaldi/gramify/internal/source"
)

// class is the charset category of a rune; a run of one class forms a segment.
type class int

// Charset classes. "other" groups everything non-alphanumeric so a run of
// specials stays a single segment.
const (
	other class = iota // anything that is not a letter or digit
	lower              // lowercase letter (also any caseless/folded letter)
	upper              // uppercase letter (purist mode only)
	digit              // Unicode digit
)

// caseSplitMin is the minimum rune-length of a camelCase token (a capital plus
// 3+ trailing lowercase) for it to be emitted as a standalone sub-word.
const caseSplitMin = 4

// classifyMode returns the charset class of a single rune.
//
// In purist mode uppercase and lowercase letters are distinct classes; in the
// default (folded) mode every letter of every script collapses to one class so
// case never causes a split.
//
// Args:
//
//	r:      the rune to classify.
//	purist: keep upper/lowercase distinct when true.
//
// Returns:
//
//	digit for Unicode digits, upper/lower for letters (folded to lower unless
//	purist), and other for everything else.
func classifyMode(r rune, purist bool) class {
	switch {
	case unicode.IsDigit(r):
		return digit
	case unicode.IsLetter(r):
		if purist && unicode.IsUpper(r) {
			return upper
		}
		return lower
	default:
		return other
	}
}

// crun is one maximal same-class run within a line, recorded as byte offsets so
// a glued segment can be sliced straight from the line with no copy.
type crun struct {
	start, end int // byte offsets into the line (end exclusive)
	runes      int // rune count of the run
	cls        class
}

// Generate reads inputPath and emits charset segments of each line.
//
// When norm is true each line is de-leetspeaked (see internal/normalize) before
// splitting. In the default mode (purist false) upper/lowercase fold together,
// a middle run of <= subThreshold runes glues its neighbours instead of
// splitting, and qualifying camelCase sub-words are emitted in addition to the
// whole. In purist mode every class change cuts and runs shorter than minLength
// are dropped. readBudget bounds the read-ahead buffers; onRead is an optional
// per-block byte progress callback (may be nil).
//
// Args:
//
//	inputPath:    the file to read.
//	readBudget:   read-ahead buffer ceiling in bytes (see source.LinesBounded).
//	onRead:       optional per-block byte progress callback; may be nil.
//	norm:         de-leetspeak each line before splitting when true.
//	minLength:    minimum emitted-segment length in runes (< 1 means 1).
//	purist:       strict per-class splitting (no folding/gluing/camelCase).
//	subThreshold: max length of a middle run treated as an insertion (0..2;
//	              forced to 0 when purist).
//	emit:         receives each segment; must be non-nil.
//
// Returns:
//
//	an error if emit is nil, or the first read/emit error; nil on success.
func Generate(inputPath string, readBudget int64, onRead func(int64), norm bool, minLength int, purist bool, subThreshold int, emit func(string) error) error {
	// precondition guards (NASA P10 rule 7: validate parameters)
	if emit == nil {
		return errors.New("cgram.Generate: nil emit")
	}
	if minLength < 1 {
		minLength = 1
	}
	if purist {
		subThreshold = 0
	}
	if subThreshold < 0 {
		subThreshold = 0
	}
	if subThreshold > 2 {
		subThreshold = 2
	}

	var runs []crun // reused across lines; bounded by line rune count (rule 2)
	return source.LinesBounded(inputPath, readBudget, onRead, func(line string) error {
		if norm {
			line = normalize.Apply(line)
		}
		if purist {
			return emitPurist(line, minLength, emit)
		}
		runs = collectRuns(line, false, runs[:0])
		return emitGlued(line, runs, subThreshold, minLength, emit)
	})
}

// emitPurist splits line at every class change (upper/lowercase distinct) and
// emits each run that is at least minLength runes — the strict --purist path.
//
// Args:
//
//	line:      the (already de-leeted, if requested) input line.
//	minLength: minimum run length in runes to emit.
//	emit:      receives each segment.
//
// Returns:
//
//	the first emit error, or nil.
func emitPurist(line string, minLength int, emit func(string) error) error {
	var buf strings.Builder
	var bufRunes int
	var cur class
	emitSeg := func() error {
		if bufRunes >= minLength {
			if err := emit(buf.String()); err != nil {
				return err
			}
		}
		buf.Reset()
		bufRunes = 0
		return nil
	}
	for _, r := range line { // bounded by rune count of the line (rule 2)
		c := classifyMode(r, true)
		if bufRunes > 0 && c != cur {
			if err := emitSeg(); err != nil {
				return err
			}
		}
		buf.WriteRune(r)
		bufRunes++
		cur = c
	}
	return emitSeg()
}

// collectRuns appends to dst (typically a reused, truncated slice) the maximal
// same-class runs of line, and returns the slice.
//
// Args:
//
//	line:   the input line.
//	purist: keep upper/lowercase distinct when true; otherwise fold all letters.
//	dst:    a slice to append runs to (pass runs[:0] to reuse the backing array).
//
// Returns:
//
//	dst with one entry per maximal same-class run.
func collectRuns(line string, purist bool, dst []crun) []crun {
	for off, r := range line { // bounded by rune count of the line (rule 2)
		c := classifyMode(r, purist)
		w := utf8.RuneLen(r)
		if n := len(dst); n > 0 && dst[n-1].cls == c {
			dst[n-1].end = off + w
			dst[n-1].runes++
			continue
		}
		dst = append(dst, crun{start: off, end: off + w, runes: 1, cls: c})
	}
	return dst
}

// walkSegments calls fn once per glued whole segment of line, cutting a segment
// boundary only where neither side is an insertion (a middle run of <=
// subThreshold runes). It is the single source of truth for segment boundaries,
// shared by the streaming emit path and the template path so they never differ.
//
// Args:
//
//	line:         the input line the run offsets index into.
//	runs:         the maximal same-class runs of line.
//	subThreshold: max length of a middle run treated as an insertion.
//	fn:           receives each whole segment (a sub-slice of line).
//
// Returns:
//
//	the first fn error, or nil.
func walkSegments(line string, runs []crun, subThreshold int, fn func(seg string) error) error {
	// ins reports whether run k is an insertion: a middle run (not first/last)
	// short enough to be a substitution rather than a real charset switch.
	ins := func(k int) bool {
		return k > 0 && k < len(runs)-1 && runs[k].runes <= subThreshold
	}
	segStart := 0
	for k := 0; k < len(runs); k++ { // bounded by len(runs) (rule 2)
		if k != len(runs)-1 && (ins(k) || ins(k+1)) {
			continue // boundary suppressed: keep accumulating into the segment
		}
		if err := fn(line[runs[segStart].start:runs[k].end]); err != nil {
			return err
		}
		segStart = k + 1
	}
	return nil
}

// emitGlued emits each whole segment of line (when >= minLength runes) and then
// its qualifying camelCase sub-words.
//
// Args:
//
//	line:         the input line the run offsets index into.
//	runs:         the maximal same-class runs of line.
//	subThreshold: max length of a middle run treated as an insertion.
//	minLength:    minimum emitted-segment length in runes.
//	emit:         receives each segment and sub-word.
//
// Returns:
//
//	the first emit error, or nil.
func emitGlued(line string, runs []crun, subThreshold, minLength int, emit func(string) error) error {
	return walkSegments(line, runs, subThreshold, func(seg string) error {
		if utf8.RuneCountInString(seg) >= minLength {
			if err := emit(seg); err != nil {
				return err
			}
		}
		return decomposeCamel(seg, minLength, emit)
	})
}

// decomposeCamel emits, for each maximal letter sub-run of seg, the camelCase
// tokens split at lowercase->uppercase boundaries whose rune-length is at least
// caseSplitMin (and minLength). The whole seg is emitted by the caller, so a
// token spanning the entire segment is skipped to avoid double-counting.
//
// Args:
//
//	seg:       a segment already emitted whole by the caller.
//	minLength: minimum emitted length in runes (the camelCase floor is the
//	           larger of this and caseSplitMin).
//	emit:      receives each qualifying sub-word.
//
// Returns:
//
//	the first emit error, or nil.
func decomposeCamel(seg string, minLength int, emit func(string) error) error {
	floor := caseSplitMin
	if minLength > floor {
		floor = minLength
	}
	tokStart, tokRunes := -1, 0
	var prevLower bool
	flush := func(end int) error {
		if tokStart < 0 || tokRunes < floor {
			return nil
		}
		if tokStart == 0 && end == len(seg) {
			return nil // identical to the whole segment the caller already emitted
		}
		return emit(seg[tokStart:end])
	}
	for off, r := range seg { // bounded by len(seg) (rule 2)
		if !unicode.IsLetter(r) { // non-letters break a letter sub-run
			if err := flush(off); err != nil {
				return err
			}
			tokStart, tokRunes, prevLower = -1, 0, false
			continue
		}
		isUpper := unicode.IsUpper(r)
		if tokStart >= 0 && isUpper && prevLower { // lowercase->uppercase: new word
			if err := flush(off); err != nil {
				return err
			}
			tokStart, tokRunes = off, 0
		}
		if tokStart < 0 {
			tokStart = off
		}
		tokRunes++
		prevLower = !isUpper
	}
	return flush(len(seg))
}

// GenerateTemplates reads inputPath and, for each non-empty line, emits the
// ordered list of its whole charset segments (the "parts" of the line). Unlike
// Generate it never drops short segments and never decomposes camelCase, so the
// parts reconstruct the line in order; the caller decides how to bucket them
// (see --beta-template). Segmentation honours norm/purist/subThreshold exactly
// as the default/purist split does.
//
// The parts slice is reused between lines, and each part (and the line itself)
// is a sub-slice of one per-line allocation; the callback must consume them
// before returning (the streaming sinks copy keys, so this is satisfied by
// routing parts straight to a sink). Because the parts partition the line
// contiguously, the caller can slice merged prefixes/suffixes from line.
//
// Args:
//
//	inputPath:    the file to read.
//	readBudget:   read-ahead buffer ceiling in bytes (see source.LinesBounded).
//	onRead:       optional per-block byte progress callback; may be nil.
//	norm:         de-leetspeak each line before splitting when true.
//	purist:       strict per-class splitting (forces subThreshold to 0).
//	subThreshold: max length of a middle run treated as an insertion (0..2).
//	emit:         receives each line and its parts (len >= 1); must be non-nil.
//
// Returns:
//
//	an error if emit is nil, or the first read/emit error; nil on success.
func GenerateTemplates(inputPath string, readBudget int64, onRead func(int64), norm, purist bool, subThreshold int, emit func(line string, parts []string) error) error {
	// precondition guards (NASA P10 rule 7: validate parameters)
	if emit == nil {
		return errors.New("cgram.GenerateTemplates: nil emit")
	}
	if purist {
		subThreshold = 0
	}
	if subThreshold < 0 {
		subThreshold = 0
	}
	if subThreshold > 2 {
		subThreshold = 2
	}

	var runs []crun     // reused across lines; bounded by line rune count
	var parts []string  // reused across lines; bounded by line run count
	return source.LinesBounded(inputPath, readBudget, onRead, func(line string) error {
		if norm {
			line = normalize.Apply(line)
		}
		runs = collectRuns(line, purist, runs[:0])
		parts = parts[:0]
		if err := walkSegments(line, runs, subThreshold, func(seg string) error {
			parts = append(parts, seg)
			return nil
		}); err != nil {
			return err
		}
		if len(parts) == 0 {
			return nil
		}
		return emit(line, parts)
	})
}
