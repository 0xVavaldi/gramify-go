package dedup

import (
	"fmt"
	"io"
	"testing"
)

// BenchmarkWriter measures the accurate stdout de-dup path (count → spill →
// key-merge) over a synthetic key stream, with a small budget to force spills.
func BenchmarkWriter(b *testing.B) {
	const n = 200_000
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("gram-%06d", i%50_000)
	}
	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()
	for it := 0; it < b.N; it++ {
		w, err := New(io.Discard, true, 4<<20, b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		for _, k := range keys {
			if err := w.Add(k); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Finalize(); err != nil {
			b.Fatal(err)
		}
	}
}
