package ngram

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkGenerate measures word-mode gram generation throughput (read +
// rolling-window emit) to a discarding sink.
func BenchmarkGenerate(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "in.txt")
	var sb strings.Builder
	for i := 0; i < 50_000; i++ {
		fmt.Fprintf(&sb, "the quick brown fox %d jumps over\n", i%1000)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(sb.Len()))
	b.ResetTimer()
	for it := 0; it < b.N; it++ {
		var count int64
		if err := Generate(path, 8<<20, nil, 1, 4, false, func(string) error { count++; return nil }); err != nil {
			b.Fatal(err)
		}
	}
}
