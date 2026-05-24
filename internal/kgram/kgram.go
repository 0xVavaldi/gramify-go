// Package kgram implements character mode.
//
// Here min/max length do NOT bound the gram length; they bound how many
// characters are *removed* from the original plaintext. For a line of length
// n and k in [min,max] (k < n):
//
//	start gram = line[:n-k]  -> the first n-k chars (last k removed).
//	             Used with hashcat -a6: candidate + k appended mask positions
//	             (?a*k) reconstructs the analysed plaintext.
//	end gram   = line[k:]    -> the last n-k chars (first k removed).
//	             Used with hashcat -a7: k prepended mask positions + candidate.
//
// Non-rolling: start and end go to separate sinks. Rolling: a single combined
// stream (start ∪ end) suitable for both -a6 and -a7 from one file.
//
// Iterative, no recursion (NASA P10 rule 1) — unlike the Python original's
// recursive kgramify_process; see docs/ANALYSIS.md §7.
package kgram

import (
	"errors"

	"github.com/Vavaldi/gramify/internal/source"
)

// Generate reads inputPath line by line and emits character k-grams: for each
// line of n runes and each k in [min,max] with k < n, the prefix line[:n-k]
// goes to start and the suffix line[k:] goes to end. For the rolling/combined
// single-stream case (e.g. --stdout), pass the same callback for both.
//
// Args:
//
//	inputPath:  the file to read.
//	readBudget: read-ahead buffer ceiling in bytes (see source.LinesBounded).
//	onRead:     optional per-block byte progress callback; may be nil.
//	min, max:   inclusive bounds on k, the number of characters removed.
//	start:      receives prefixes (for hashcat -a6); nil skips the start half.
//	end:        receives suffixes (for hashcat -a7); nil skips the end half.
//
// At least one of start/end must be non-nil. A nil half is neither computed nor
// emitted, so --rolling-start / --rolling-end do no wasted work.
//
// Returns:
//
//	an error for invalid parameters, or the first read/emit error; nil on
//	success.
func Generate(inputPath string, readBudget int64, onRead func(int64), min, max int, start, end func(string) error) error {
	// precondition guards (NASA P10 rule 7: validate parameters)
	if start == nil && end == nil {
		return errors.New("kgram.Generate: both start and end are nil")
	}
	if min < 1 || max < min {
		return errors.New("kgram.Generate: require 1 <= min <= max")
	}

	return source.LinesBounded(inputPath, readBudget, onRead, func(line string) error {
		r := []rune(line)
		n := len(r)
		// bounded: k runs min..max and breaks once k >= n (rule 2)
		for k := min; k <= max; k++ {
			if k >= n { // need at least one char remaining
				break
			}
			if start != nil {
				if err := start(string(r[:n-k])); err != nil {
					return err
				}
			}
			if end != nil {
				if err := end(string(r[k:])); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
