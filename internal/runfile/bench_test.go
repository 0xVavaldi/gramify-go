package runfile

import (
	"fmt"
	"path/filepath"
	"testing"
)

// BenchmarkMerge measures k-way merge throughput across run counts. Counts
// above maxFanIn exercise the cascaded multi-pass path (P3); the budget is kept
// small so per-run buffers would otherwise collapse to the floor.
func BenchmarkMerge(b *testing.B) {
	for _, runs := range []int{64, 256, 1024} {
		b.Run(fmt.Sprintf("runs=%d", runs), func(b *testing.B) {
			dir := b.TempDir()
			const perRun = 2000
			paths := make([]string, 0, runs)
			for r := 0; r < runs; r++ {
				p := filepath.Join(dir, fmt.Sprintf("r%05d.run", r))
				if err := Write(p, func(emit func(string, uint64) error) error {
					for i := 0; i < perRun; i++ { // keys increase with i => sorted
						if err := emit(fmt.Sprintf("k%09d", r+i*runs), 1); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					b.Fatal(err)
				}
				paths = append(paths, p)
			}
			b.SetBytes(int64(runs * perRun))
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				var count int64
				err := Merge(paths, 64<<10, dir, nil,
					func(a, c Record) bool { return a.Key < c.Key },
					func(string, uint64) error { count++; return nil })
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
