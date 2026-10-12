// Command gramify is the CLI entry point. It parses arguments, dispatches to
// the word / character / charset generator, frequency-sorts the output
// externally, and writes it to a file or stdout.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"unicode"

	"github.com/Vavaldi/gramify/internal/cgram"
	"github.com/Vavaldi/gramify/internal/cli"
	"github.com/Vavaldi/gramify/internal/dedup"
	"github.com/Vavaldi/gramify/internal/freqsort"
	"github.com/Vavaldi/gramify/internal/humanize"
	"github.com/Vavaldi/gramify/internal/kgram"
	"github.com/Vavaldi/gramify/internal/membudget"
	"github.com/Vavaldi/gramify/internal/ngram"
	"github.com/Vavaldi/gramify/internal/progress"
)

// main parses arguments and runs the selected mode.
//
// Exit codes: 0 success, 2 usage/parse error, 1 runtime error. -h/--help
// prints usage and exits 0.
func main() {
	cfg, err := cli.Parse(os.Args[1:])
	if err != nil {
		if cli.IsHelp(err) {
			fmt.Println(cli.Usage)
			return
		}
		if cli.IsVersion(err) {
			fmt.Println("gramify", cli.Version)
			return
		}
		if cli.IsStdoutAggregation(err) {
			// Show the notice verbatim (no usage dump) and stop.
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if cli.IsInputMissing(err) {
			// A wrong path isn't a usage error; the full help would be noise.
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, "\n"+cli.Usage)
		os.Exit(2)
	}

	budget := resolveBudget(cfg)
	// Apply the ceiling as the Go soft memory limit: the runtime GCs harder as
	// usage approaches it, providing the "little flexibility" backstop on top
	// of our explicitly-sized read and processing buffers.
	debug.SetMemoryLimit(budget.Max)

	if cfg.Debug {
		fmt.Fprintf(os.Stderr, "debug: memory budget %s (read %s + proc %s buffers; %s reserved as GC headroom)\n",
			humanize.Bytes(float64(budget.Max)), humanize.Bytes(float64(budget.Read)),
			humanize.Bytes(float64(budget.Proc)), humanize.Bytes(float64(budget.Max-budget.Read-budget.Proc)))
	}

	stopCPU := startCPUProfile(cfg.CPUProfile)
	runErr := run(cfg, budget)
	stopCPU() // flush profile before any os.Exit (which skips defers)
	writeMemProfile(cfg.MemProfile)

	if cfg.Debug {
		reportRAM()
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "error:", runErr)
		os.Exit(1)
	}
}

// startCPUProfile begins CPU profiling to path (no-op when empty) and returns a
// function that stops profiling and closes the file. The returned func must be
// called before any os.Exit, which skips deferred calls.
func startCPUProfile(path string) func() {
	if path == "" {
		return func() {}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: cpuprofile:", err)
		return func() {}
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, "warning: cpuprofile:", err)
		f.Close()
		return func() {}
	}
	return func() { pprof.StopCPUProfile(); f.Close() }
}

// writeMemProfile writes a heap profile to path (no-op when empty), after a GC
// so the live-heap numbers are settled.
func writeMemProfile(path string) {
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: memprofile:", err)
		return
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, "warning: memprofile:", err)
	}
}

// reportRAM prints how much RAM the run actually used: the OS peak working set
// (true physical peak) when available, otherwise the Go runtime's in-use heap
// (not a peak; Sys is reserved virtual space, not physical RAM).
func reportRAM() {
	if peak, ok := membudget.PeakRSS(); ok {
		fmt.Fprintf(os.Stderr, "debug: peak RAM %s\n", humanize.Bytes(float64(peak)))
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(os.Stderr, "debug: Go heap in use %s (reserved %s)\n",
		humanize.Bytes(float64(ms.HeapInuse)), humanize.Bytes(float64(ms.Sys)))
}

// resolveBudget turns cfg.MemoryMax into a read/processing split, deriving the
// default (a quarter of system RAM) when the flag was omitted.
//
// Args:
//
//	cfg: the validated configuration.
//
// Returns:
//
//	the memory Budget to apply for this run.
func resolveBudget(cfg *cli.Config) membudget.Budget {
	max := cfg.MemoryMax
	if max <= 0 {
		detected := false
		max, detected = membudget.Default()
		if !detected {
			fmt.Fprintf(os.Stderr,
				"note: could not detect system RAM; defaulting --memory-max to %d bytes\n", max)
		}
	}
	if max < membudget.MinBudget {
		fmt.Fprintf(os.Stderr,
			"note: --memory-max raised to the %d-byte minimum\n", membudget.MinBudget)
	}
	return membudget.Split(max)
}

// startProgress builds a progress reporter for a run. A bar is shown only when
// output is not streaming to stdout and stderr is an interactive terminal
// (so a redirected stderr is not polluted with carriage returns).
//
// Args:
//
//	cfg: the validated configuration.
//
// Returns:
//
//	onRead, a per-block byte callback to pass to the generator (nil when no bar
//	is shown), and finish, which completes the bar (a no-op when none is shown).
func startProgress(cfg *cli.Config) (onRead func(int64), finish func()) {
	if cfg.Stdout || !isTerminal(os.Stderr) {
		return nil, func() {}
	}
	fi, err := os.Stat(cfg.Input)
	if err != nil {
		return nil, func() {}
	}
	bar := progress.New(os.Stderr, fi.Size(), "processing")
	return bar.Add, bar.Finish
}

// isTerminal reports whether f is a character device (an interactive terminal)
// rather than a file or pipe. This dependency-free check works on Windows and
// Unix alike.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// sink is one output stream. add receives grams; finish flushes/closes on
// success; cancel releases resources on an aborted run.
type sink struct {
	add    func(string) error
	finish func() error
	cancel func()
}

// newSink builds the right backing store for path:
//   - non-weighted stdout (path == "") with no count filtering: streaming
//     dedup writer (in-memory cache unless cfg.DisableCache).
//   - file output, --weighted, or --min-freq > 1: disk-backed freqsort
//     pipeline (count-based filtering needs aggregation).
//
// For file output on an interactive terminal a separate "sorting" progress bar
// tracks the freqsort finalize phase.
func newSink(cfg *cli.Config, path string, procBudget int64, quiet bool, pool *freqsort.Pool) (*sink, error) {
	if path == "" && !cfg.Weighted && cfg.MinFreq <= 1 {
		dw, err := dedup.New(os.Stdout, !cfg.DisableCache, procBudget, cfg.CacheDir)
		if err != nil {
			return nil, err
		}
		return &sink{add: dw.Add, finish: dw.Finalize, cancel: dw.Close}, nil
	}

	var w io.Writer
	var closer func() error
	if path == "" {
		w = os.Stdout
		closer = func() error { return nil }
	} else {
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		w = f
		closer = f.Close
		if !quiet {
			fmt.Fprintln(os.Stderr, "Writing output to:", path)
		}
	}
	// Charset grams default to case-insensitive frequency grouping (most-frequent
	// casing wins); --case-sensitive and the other modes keep exact-case counting.
	caseInsensitive := cfg.Mode == cli.ModeCharset && !cfg.CaseSensitive
	s, err := freqsort.New(w, cfg.Weighted, uint64(cfg.MinFreq), procBudget, cfg.CacheDir, caseInsensitive)
	if err != nil {
		closer()
		return nil, err
	}
	s.UsePool(pool) // shared memory budget across template sinks (nil = solo)
	var sortBar *progress.Bar
	if path != "" && !quiet && isTerminal(os.Stderr) {
		sortBar = progress.NewCounter(os.Stderr, "sorting ")
		s.SetProgress(sortBar)
	}
	return &sink{
		add: s.Add,
		finish: func() error {
			ferr := s.Finalize()
			if sortBar != nil {
				sortBar.Finish()
			}
			if ferr != nil {
				closer()
				return ferr
			}
			return closer()
		},
		cancel: func() {
			// abort path: best-effort cleanup; the run already failed so
			// the close error is intentionally discarded (NASA P10 rule 7).
			s.Close()
			_ = closer()
		},
	}, nil
}

// run dispatches a validated Config to the matching generator and drives its
// output sink.
//
// Args:
//
//	cfg: the validated configuration (must be non-nil).
//
// Returns:
//
//	the first error from sink setup, generation, or finalisation; nil on
//	success.
func run(cfg *cli.Config, budget membudget.Budget) error {
	if cfg == nil { // precondition guard (NASA P10 rule 7)
		return fmt.Errorf("run: nil config")
	}
	onRead, finish := startProgress(cfg)
	switch cfg.Mode {
	case cli.ModeWord:
		sk, err := newSink(cfg, outPath(cfg, "n_"+cfg.Output), budget.Proc, false, nil)
		if err != nil {
			return err
		}
		err = ngram.Generate(cfg.Input, budget.Read, onRead, cfg.MinLength, cfg.MaxLength, sk.add)
		finish()
		if err != nil {
			sk.cancel()
			return err
		}
		return sk.finish()

	case cli.ModeCharset:
		if cfg.BetaTemplate {
			return runCharsetTemplates(cfg, budget, onRead, finish)
		}
		sk, err := newSink(cfg, outPath(cfg, "c_"+cfg.Output), budget.Proc, false, nil)
		if err != nil {
			return err
		}
		emit := sk.add
		if cfg.Mixed {
			emit = mixedEmit(emit)
		}
		err = cgram.Generate(cfg.Input, budget.Read, onRead, cfg.Normalize, cfg.MinLength,
			cfg.Purist, cfg.SubLength, emit)
		finish()
		if err != nil {
			sk.cancel()
			return err
		}
		return sk.finish()

	case cli.ModeCharacter:
		return runCharacter(cfg, budget, onRead, finish)
	}
	return fmt.Errorf("unhandled mode")
}

// runCharacter handles character mode, which has two output shapes.
//
// Args:
//
//	cfg: the validated configuration.
//
// Returns:
//
//	an error from sink setup, generation, or finalisation; nil on success.
//
// A single stream is used for any --rolling* variant or --stdout (stdout cannot
// be split into two files); otherwise start (-a6) and end (-a7) go to separate
// k_start./k_end. files.
func runCharacter(cfg *cli.Config, budget membudget.Budget, onRead func(int64), finish func()) error {
	// Single stream when any rolling variant is set (cli forces plain --rolling
	// for stdout, which can't be split into two files).
	if cfg.Rolling || cfg.RollingStart || cfg.RollingEnd {
		return runCharacterRolling(cfg, budget, onRead, finish)
	}

	// Split: start (-a6) and end (-a7) into separate files. Each sink gets half
	// the processing budget since both buffer concurrently.
	half := budget.Proc / 2
	startSink, err := newSink(cfg, "k_start."+cfg.Output, half, false, nil)
	if err != nil {
		return err
	}
	endSink, err := newSink(cfg, "k_end."+cfg.Output, half, false, nil)
	if err != nil {
		startSink.cancel()
		return err
	}
	err = kgram.Generate(cfg.Input, budget.Read, onRead, cfg.MinLength, cfg.MaxLength, startSink.add, endSink.add)
	finish()
	if err != nil {
		startSink.cancel()
		endSink.cancel()
		return err
	}
	if err := startSink.finish(); err != nil {
		endSink.cancel()
		return err
	}
	return endSink.finish()
}

// runCharacterRolling handles the single-stream character shapes: --rolling
// (start∪end), --rolling-start (prefixes only, -a6) and --rolling-end (suffixes
// only, -a7). The unwanted half, if any, is discarded by a no-op callback.
//
// Args:
//
//	cfg: the validated configuration (exactly one rolling variant set).
//
// Returns:
//
//	an error from sink setup, generation, or finalisation; nil on success.
func runCharacterRolling(cfg *cli.Config, budget membudget.Budget, onRead func(int64), finish func()) error {
	name, wantStart, wantEnd := "k_rolling.", true, true
	switch {
	case cfg.RollingStart:
		name, wantEnd = "k_rolling-start.", false
	case cfg.RollingEnd:
		name, wantStart = "k_rolling-end.", false
	}
	path := ""
	if !cfg.Stdout {
		path = name + cfg.Output
	}
	sk, err := newSink(cfg, path, budget.Proc, false, nil)
	if err != nil {
		return err
	}
	// A nil callback tells kgram to skip that half entirely (no wasted work).
	var start, end func(string) error
	if wantStart {
		start = sk.add
	}
	if wantEnd {
		end = sk.add
	}
	err = kgram.Generate(cfg.Input, budget.Read, onRead, cfg.MinLength, cfg.MaxLength, start, end)
	finish()
	if err != nil {
		sk.cancel()
		return err
	}
	return sk.finish()
}

// outPath returns the destination path for a sink: "" (stdout) when
// cfg.Stdout is set, otherwise filePath.
//
// Args:
//
//	cfg:      the validated configuration.
//	filePath: the file path to use when not writing to stdout.
//
// Returns:
//
//	"" for stdout, or filePath for file output.
func outPath(cfg *cli.Config, filePath string) string {
	if cfg.Stdout {
		return ""
	}
	return filePath
}

// mixedEmit wraps an emit function to also emit the lowercased version of any
// segment that contains mixed-case letters (both upper and lower).
func mixedEmit(emit func(string) error) func(string) error {
	return func(s string) error {
		if err := emit(s); err != nil {
			return err
		}
		hasUpper, hasLower := false, false
		for _, r := range s {
			if unicode.IsUpper(r) {
				hasUpper = true
			} else if unicode.IsLower(r) {
				hasLower = true
			}
			if hasUpper && hasLower {
				return emit(strings.ToLower(s))
			}
		}
		return nil
	}
}

// maxTemplateParts caps the segments per line that --beta-template buckets. A
// line with more parts is skipped: it bounds the number of distinct output
// files (at most maxTemplateParts*(maxTemplateParts+1)/2 + 2) and the file
// handles/budget held open at once (NASA P10 rule 2).
const maxTemplateParts = 64

// maxTemplateRunBytes caps an individual template sink's in-memory buffer so no
// single map grows huge (which would make its sort and GC scan costly); the
// shared pool still bounds the combined total. Spilled runs stay around this
// size — large enough that the merge isn't dominated by tiny runs.
const maxTemplateRunBytes = 32 << 20 // 32 MiB

// runCharsetTemplates implements --beta-template: split each line into its n
// charset parts and route part id (1-based) of an n-part line to
// <dir>/c_<n>_<id>.txt, every first part to c_start.txt and every last part to
// c_end.txt. Each file is an independent dedup+freq-sorted sink.
//
// Args:
//
//	cfg:    the validated configuration (BetaTemplate set, file output).
//	budget: the resolved memory budget; split across the open sinks.
//	onRead: per-block read progress callback (may be nil).
//	finish: completes the input progress bar.
//
// Returns:
//
//	the first error from folder creation, generation, or finalisation; nil on
//	success.
func runCharsetTemplates(cfg *cli.Config, budget membudget.Budget, onRead func(int64), finish func()) error {
	dir := "c_" + sanitizeName(cfg.Output) + "_templates"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// A shared pool caps the combined in-memory buffers of all sinks at
	// budget.Proc (spilling the largest when over), so busy sinks use most of the
	// budget and inputs that fit never spill. Each sink also caps its own buffer
	// at maxTemplateRunBytes so no single map grows huge; at tiny budgets the
	// pool binds first (perSink is clamped to proc).
	perSink := int64(maxTemplateRunBytes)
	if perSink > budget.Proc {
		perSink = budget.Proc
	}
	pool := freqsort.NewPool(budget.Proc)
	tw := &templateWriter{cfg: cfg, dir: dir, perSink: perSink, pool: pool}
	err := cgram.GenerateTemplates(cfg.Input, budget.Read, onRead,
		cfg.Normalize, cfg.Purist, cfg.SubLength, tw.addLine)
	finish()
	if err != nil {
		tw.cancel()
		return err
	}
	if err := tw.finish(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Wrote %d template files to: %s%c\n", len(tw.all), dir, os.PathSeparator)
	return nil
}

// templateWriter routes line parts to per-bucket freqsort sinks, created lazily.
// Sinks are addressed by integer index (part[n][i], start[k], end[k]) rather
// than a formatted name, so the hot path does no per-line fmt.Sprintf or map
// lookup — that allocation churn otherwise dominates GC under a tight budget.
// all keeps every sink in creation order for finalisation/cleanup.
type templateWriter struct {
	cfg     *cli.Config
	dir     string
	perSink int64
	pool    *freqsort.Pool // shared in-memory budget across all sinks
	part    [][]*sink      // part[n][i]: id i (0-based) of an n-part line
	start   []*sink        // start[k]: c_start_<k>.txt (k >= 1)
	end     []*sink        // end[k]:   c_end_<k>.txt   (k >= 1)
	all     []*sink        // every created sink, in creation order
}

// addLine buckets one line's parts. Part i of an n-part line goes to
// c_<n>_<i>.txt. It also writes merged prefix/suffix bundles: the first k parts
// to c_start_<k>.txt and the last k to c_end_<k>.txt, for every width k in
// 1..n-1 (k stays below n, so an n-part line never produces width n; a 1-part
// line produces no bundles). Over-long lines are skipped. The merged bundles are
// zero-copy slices of line, since parts partition it contiguously.
//
// Args:
//
//	line:  the (possibly normalised) source line the parts slice into.
//	parts: the line's ordered charset segments (len >= 1).
//
// Returns:
//
//	the first sink error, or nil.
func (t *templateWriter) addLine(line string, parts []string) error {
	n := len(parts)
	if n == 0 || n > maxTemplateParts {
		return nil
	}
	for len(t.part) <= n { // grow the per-n row table
		t.part = append(t.part, nil)
	}
	if t.part[n] == nil {
		t.part[n] = make([]*sink, n)
	}
	for i, p := range parts { // bounded by n <= maxTemplateParts (rule 2)
		s, err := t.sinkAt(&t.part[n][i], "c_", n, i+1)
		if err != nil {
			return err
		}
		if err := s.add(p); err != nil {
			return err
		}
	}
	pre, suf := 0, len(line) // byte cursors for the first-k / last-k bundles
	for k := 1; k < n; k++ { // widths 1..n-1, bounded by n (rule 2)
		pre += len(parts[k-1])
		s, err := t.bundleAt(&t.start, k, "c_start_")
		if err != nil {
			return err
		}
		if err := s.add(line[:pre]); err != nil {
			return err
		}
		suf -= len(parts[n-k])
		e, err := t.bundleAt(&t.end, k, "c_end_")
		if err != nil {
			return err
		}
		if err := e.add(line[suf:]); err != nil {
			return err
		}
	}
	return nil
}

// sinkAt returns the part sink at *slot, creating its c_<n>_<id>.txt file on
// first use (the only place a name is formatted, ~once per output file).
func (t *templateWriter) sinkAt(slot **sink, prefix string, n, id int) (*sink, error) {
	if *slot == nil {
		s, err := t.create(fmt.Sprintf("%s%d_%d.txt", prefix, n, id))
		if err != nil {
			return nil, err
		}
		*slot = s
	}
	return *slot, nil
}

// bundleAt returns the width-k bundle sink in *tbl (start or end), growing the
// table and creating the <prefix><k>.txt file on first use.
func (t *templateWriter) bundleAt(tbl *[]*sink, k int, prefix string) (*sink, error) {
	for len(*tbl) <= k {
		*tbl = append(*tbl, nil)
	}
	if (*tbl)[k] == nil {
		s, err := t.create(fmt.Sprintf("%s%d.txt", prefix, k))
		if err != nil {
			return nil, err
		}
		(*tbl)[k] = s
	}
	return (*tbl)[k], nil
}

// create opens a new (quiet) freqsort sink for name inside the template folder.
func (t *templateWriter) create(name string) (*sink, error) {
	s, err := newSink(t.cfg, filepath.Join(t.dir, name), t.perSink, true, t.pool)
	if err != nil {
		return nil, err
	}
	t.all = append(t.all, s)
	return s, nil
}

// finish finalises every sink. On the first failure the remaining sinks are
// cancelled (best-effort) and the error returned.
func (t *templateWriter) finish() error {
	var firstErr error
	for _, s := range t.all { // bounded by len(t.all) (rule 2)
		if firstErr != nil {
			s.cancel()
			continue
		}
		if err := s.finish(); err != nil {
			firstErr = err
		}
	}
	return firstErr
}

// cancel aborts every sink (best-effort cleanup on a failed run).
func (t *templateWriter) cancel() {
	for _, s := range t.all { // bounded by len(t.all) (rule 2)
		s.cancel()
	}
}

// sanitizeName reduces a name (the output argument) to one safe filename
// component for the template folder: the base name with its final extension
// dropped and every character outside [A-Za-z0-9._-] replaced by '_', guarding
// against path traversal (directory components are dropped) and empty/dot
// results. Both '/' and '\' are treated as separators on every OS, so a
// Windows-style path is reduced to its base even on Linux (filepath.Base alone
// would not split on '\' there).
//
// Args:
//
//	name: the output file name (or path) to derive the folder from.
//
// Returns:
//
//	a non-empty, traversal-safe single path component.
func sanitizeName(name string) string {
	base := strings.TrimRight(name, `/\`) // ignore trailing separators
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:] // keep only the last path element
	}
	base = strings.TrimSuffix(base, filepath.Ext(base)) // drop the final extension
	var b strings.Builder
	for _, r := range base { // bounded by len(base) (rule 2)
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	clean := b.String()
	if clean == "" || clean == "." || clean == ".." {
		return "input"
	}
	return clean
}
