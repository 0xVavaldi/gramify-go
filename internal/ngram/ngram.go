// Package ngram implements word mode: contiguous word-window N-grams over the
// whitespace-flattened input (port of gramify.py's ngramify, base behaviour
// only — no --ngram-more). Iterative, no recursion (NASA P10 rule 1).
package ngram

import (
	"errors"
	"strings"
	"unicode"

	"github.com/Vavaldi/gramify/internal/source"
)

// stripSpecialMap is the Unicode-aware "delete special characters" rune mapping:
// letters and digits pass through unchanged, any whitespace becomes ASCII space
// (so tabs/CRs act as token separators rather than glue), and every other rune
// is dropped — so "Don't go!" -> "Dont go", "state-of-the-art" -> "stateoftheart".
func stripSpecialMap(r rune) rune {
	switch {
	case unicode.IsLetter(r), unicode.IsDigit(r):
		return r
	case unicode.IsSpace(r):
		return ' '
	default:
		return -1 // drop
	}
}

// Generate reads inputPath, splits every line on single spaces (dropping empty
// tokens, mirroring the Python `filter(None, split(" "))`), and emits every
// contiguous window of min..max words joined by a single space.
//
// Output streams as the input is read: a rolling buffer of at most `max`
// tokens is kept, and once it is full every window starting at the oldest
// token (lengths min..max) is emitted before the oldest token is dropped and
// the buffer slides. This bounds memory to `max` tokens (instead of the whole
// file) and avoids the start-up delay of buffering the entire input. Windows
// still span input line boundaries, exactly as before; only the emission order
// changes (start-major rather than gram-length-major).
//
// Args:
//
//	inputPath:    the file to read.
//	readBudget:   read-ahead buffer ceiling in bytes (see source.LinesBounded).
//	onRead:       optional per-block byte progress callback; may be nil.
//	min, max:     inclusive window-length bounds in words.
//	stripSpecial: when true, drop every rune that is not a Unicode letter,
//	              digit, or whitespace before tokenising (e.g. "Don't go!" ->
//	              "Dont go"); whitespace is normalised to a single space so
//	              tabs/CRs act as separators instead of glue.
//	emit:         receives each generated gram; its first error aborts.
//
// Returns:
//
//	the first read or emit error; nil on success.
func Generate(inputPath string, readBudget int64, onRead func(int64), min, max int, stripSpecial bool, emit func(string) error) error {
	// precondition guards (NASA P10 rule 7: validate parameters)
	if emit == nil {
		return errors.New("ngram.Generate: nil emit")
	}
	if min < 1 || max < min {
		return errors.New("ngram.Generate: require 1 <= min <= max")
	}

	window := make([]string, 0, max)

	// emitFrom emits every window starting at window[0] with length min..len.
	// emitFrom emits every window starting at window[0], lengths min..len(window).
	emitFrom := func() error {
		for l := min; l <= len(window); l++ { // bounded by len(window) (rule 2)
			if err := emit(strings.Join(window[:l], " ")); err != nil {
				return err
			}
		}
		return nil
	}
	// dropOldest removes window[0], sliding the buffer forward by one token.
	dropOldest := func() {
		copy(window, window[1:])
		window = window[:len(window)-1]
	}
	// feed appends one token; once the window is full it emits the oldest
	// token's windows (now fully determined) and drops it.
	feed := func(tok string) error {
		window = append(window, tok)
		if len(window) < max {
			return nil
		}
		if err := emitFrom(); err != nil { // oldest token's windows are settled
			return err
		}
		dropOldest()
		return nil
	}

	err := source.LinesBounded(inputPath, readBudget, onRead, func(line string) error {
		if stripSpecial {
			line = strings.Map(stripSpecialMap, line)
		}
		for _, tok := range strings.Split(line, " ") { // bounded by line length
			if tok == "" {
				continue
			}
			if ferr := feed(tok); ferr != nil {
				return ferr
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Drain the tail: each remaining start position emits windows of length
	// min..(tokens remaining from it).
	for len(window) > 0 { // bounded by max (rule 2)
		if err := emitFrom(); err != nil {
			return err
		}
		dropOldest()
	}
	return nil
}
