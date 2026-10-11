package freqsort

import (
	"fmt"
	"io"
	"testing"
)

// BenchmarkSorter measures end-to-end frequency sorting (count → spill → merge
// → count-sort) over a synthetic key stream. The small budget forces spilling
// so the disk path is exercised.
func BenchmarkSorter(b *testing.B) {
	const n = 200_000
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("gram-%06d", i%50_000) // ~4x duplication
	}
	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()
	for it := 0; it < b.N; it++ {
		s, err := New(io.Discard, false, 1, 4<<20, b.TempDir(), false)
		if err != nil {
			b.Fatal(err)
		}
		for _, k := range keys {
			if err := s.Add(k); err != nil {
				b.Fatal(err)
			}
		}
		if err := s.Finalize(); err != nil {
			b.Fatal(err)
		}
	}
}
