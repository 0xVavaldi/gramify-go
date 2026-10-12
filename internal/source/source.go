// Package source streams an input file line by line, stripping trailing
// CR/LF, mirroring gramify.py's `open(..., errors="ignore")` + `rstrip("\r\n")`.
//
// LinesBounded overlaps IO with processing: a reader goroutine fills a fixed
// pool of byte blocks (sized to a read budget) while the caller's goroutine
// consumes them, so CPU work never stalls the disk read. The pool caps the
// memory the reader can hold in flight.
package source

import (
	"bytes"
	"errors"
	"io"
	"os"
)

// defaultReadBudget bounds the read-ahead pool for the convenience Lines
// wrapper (callers that don't thread a --memory-max budget).
const defaultReadBudget = 8 << 20 // 8 MiB

// Lines calls fn for every line of the file at path with a default read
// budget. See LinesBounded for the streaming/buffering contract.
func Lines(path string, fn func(line string) error) error {
	return LinesBounded(path, defaultReadBudget, nil, fn)
}

// LinesBounded streams the file at path to fn one line at a time (trailing
// "\r"/"\n" removed; a final newline-less line is still delivered), using a
// read-ahead block pool bounded by readBudget bytes.
//
// Args:
//
//	path:        the input file.
//	readBudget:  approximate ceiling for read-ahead buffers, in bytes.
//	onRead:      optional callback invoked with the byte count of each block as
//	             it is consumed (for progress reporting); may be nil.
//	fn:          per-line callback; its first error aborts the stream.
//
// Returns:
//
//	the first callback error, or the first read error; nil on success.
func LinesBounded(path string, readBudget int64, onRead func(n int64), fn func(line string) error) error {
	// precondition guards (NASA P10 rule 7: validate parameters)
	if path == "" {
		return errors.New("source.LinesBounded: empty path")
	}
	if fn == nil {
		return errors.New("source.LinesBounded: nil callback")
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	blockSize, poolDepth := planBuffers(readBudget)

	type block struct {
		buf []byte
		n   int
	}
	filled := make(chan *block, poolDepth)
	free := make(chan *block, poolDepth)
	for i := 0; i < poolDepth; i++ { // bounded by poolDepth (rule 2)
		free <- &block{buf: make([]byte, blockSize)}
	}

	// stop lets the consumer abort the reader without leaking it; readErr is
	// published before filled is closed (channel close = happens-before).
	stop := make(chan struct{})
	var readErr error
	go func() {
		defer close(filled)
		for { // bounded: each iteration reads >=1 byte or returns at EOF/err
			var b *block
			select {
			case b = <-free:
			case <-stop:
				return
			}
			n, rerr := f.Read(b.buf)
			if n > 0 {
				b.n = n
				select {
				case filled <- b:
				case <-stop:
					return
				}
			}
			if rerr != nil {
				if rerr != io.EOF {
					readErr = rerr
				}
				return
			}
		}
	}()

	// drainStop aborts the reader and waits for it to finish so no goroutine
	// is left blocked on the free/filled channels.
	drainStop := func() {
		close(stop)
		for range filled { // discard any in-flight blocks until reader closes
		}
	}

	var carry []byte // owned buffer holding a partial line across blocks
	emit := func(seg []byte) error {
		if k := len(seg); k > 0 && seg[k-1] == '\r' {
			seg = seg[:k-1]
		}
		return fn(string(seg)) // string() copies, so blocks are safe to recycle
	}

	for b := range filled { // bounded by the number of blocks read (rule 2)
		data := b.buf[:b.n]
		start := 0
		for { // bounded by the newline count in data (rule 2)
			nl := bytes.IndexByte(data[start:], '\n')
			if nl < 0 {
				break
			}
			end := start + nl
			var cerr error
			if len(carry) > 0 {
				carry = append(carry, data[start:end]...)
				cerr = emit(carry)
				carry = carry[:0]
			} else {
				cerr = emit(data[start:end])
			}
			if cerr != nil {
				drainStop()
				return cerr
			}
			start = end + 1
		}
		if start < len(data) { // trailing partial line -> carry to next block
			carry = append(carry, data[start:]...)
		}
		if onRead != nil {
			onRead(int64(b.n)) // report bytes processed (drives the progress bar)
		}
		free <- b
	}

	if len(carry) > 0 { // final line without a trailing newline
		if err := emit(carry); err != nil {
			return err
		}
	}
	return readErr
}

// planBuffers picks a block size and pool depth whose product stays within
// readBudget, while keeping blocks in a sane 64 KiB..16 MiB range and the pool
// at least two deep (so the reader can fill one block while another is
// processed).
//
// Args:
//
//	readBudget: the read-side byte budget.
//
// Returns:
//
//	the per-block size in bytes and the number of blocks in the pool.
func planBuffers(readBudget int64) (blockSize, poolDepth int) {
	const minBlock = 64 << 10
	const maxBlock = 16 << 20
	bs := readBudget / 8
	if bs < minBlock {
		bs = minBlock
	}
	if bs > maxBlock {
		bs = maxBlock
	}
	depth := readBudget / bs
	if depth < 2 {
		depth = 2
	}
	return int(bs), int(depth)
}
