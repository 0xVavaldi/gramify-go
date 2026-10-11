package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// withInput returns args with a real temp input file prepended after the
// subcommand, so validate's os.Stat succeeds.
func withInput(t *testing.T, sub string, rest ...string) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(p, []byte("a b c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return append([]string{sub, p}, rest...)
}

// TestStdoutAggregationRefused: --min-freq or --weighted with --stdout (no
// override) refuses to run, returning the dedicated sentinel error. This also
// covers the implicit-stdout default (no output file given).
func TestStdoutAggregationRefused(t *testing.T) {
	cases := [][]string{
		{"word", "--stdout", "--min-freq=3"},
		{"word", "--stdout", "--weighted"},
		{"word", "--min-freq=3"}, // implicit stdout (no output file)
		{"word", "--weighted"},   // implicit stdout
	}
	for _, args := range cases {
		_, err := Parse(withInput(t, args[0], args[1:]...))
		if !IsStdoutAggregation(err) {
			t.Errorf("%v: want ErrStdoutAggregation, got %v", args, err)
		}
	}
}

// TestMinFreqFileOutputKept: a >1 min-freq survives for file output, in any
// mode (not just charset).
func TestMinFreqFileOutputKept(t *testing.T) {
	for _, sub := range []string{"word", "character", "charset"} {
		cfg, err := Parse(withInput(t, sub, "out", "--min-freq=5"))
		if err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
		if cfg.MinFreq != 5 {
			t.Errorf("%s: MinFreq = %d, want 5", sub, cfg.MinFreq)
		}
	}
}

// TestMinFreqDefaults: file output defaults to 2 (drop singletons); stdout is
// forced to 1 (no filtering) and a plain stdout run is not refused.
func TestMinFreqDefaults(t *testing.T) {
	fileCfg, err := Parse(withInput(t, "word", "out"))
	if err != nil {
		t.Fatal(err)
	}
	if fileCfg.MinFreq != 2 {
		t.Errorf("file output default MinFreq = %d, want 2", fileCfg.MinFreq)
	}

	outCfg, err := Parse(withInput(t, "word", "--stdout"))
	if err != nil {
		t.Fatalf("plain --stdout should not be refused: %v", err)
	}
	if outCfg.MinFreq != 1 {
		t.Errorf("stdout MinFreq = %d, want 1 (no filtering)", outCfg.MinFreq)
	}

	implicit, err := Parse(withInput(t, "word")) // no output -> implicit stdout
	if err != nil {
		t.Fatalf("implicit stdout should not be refused: %v", err)
	}
	if implicit.MinFreq != 1 {
		t.Errorf("implicit stdout MinFreq = %d, want 1", implicit.MinFreq)
	}
}

// TestCharacterStdoutImpliesRolling: character mode on stdout (explicit or
// implicit) auto-enables rolling; file output leaves it off unless requested.
func TestCharacterStdoutImpliesRolling(t *testing.T) {
	for _, args := range [][]string{{"character", "--stdout"}, {"character"}} {
		cfg, err := Parse(withInput(t, args[0], args[1:]...))
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !cfg.Rolling {
			t.Errorf("%v: Rolling should be auto-enabled on stdout", args)
		}
	}
	fileCfg, err := Parse(withInput(t, "character", "out"))
	if err != nil {
		t.Fatal(err)
	}
	if fileCfg.Rolling {
		t.Error("file output should not auto-enable Rolling")
	}
}

// TestRollingHalves: --rolling-start / --rolling-end are accepted in character
// mode (to a file), but rolling variants are mutually exclusive and invalid
// outside character mode.
func TestRollingHalves(t *testing.T) {
	start, err := Parse(withInput(t, "character", "out", "--rolling-start"))
	if err != nil {
		t.Fatalf("--rolling-start: %v", err)
	}
	if !start.RollingStart || start.RollingEnd || start.Rolling {
		t.Errorf("--rolling-start flags = %+v", start)
	}
	end, err := Parse(withInput(t, "character", "out", "--rolling-end"))
	if err != nil {
		t.Fatalf("--rolling-end: %v", err)
	}
	if !end.RollingEnd || end.RollingStart || end.Rolling {
		t.Errorf("--rolling-end flags = %+v", end)
	}

	// Mutually exclusive combinations are rejected.
	bad := [][]string{
		{"character", "out", "--rolling-start", "--rolling-end"},
		{"character", "out", "--rolling", "--rolling-start"},
		{"character", "out", "--rolling", "--rolling-end"},
		{"word", "out", "--rolling-start"}, // wrong mode
	}
	for _, args := range bad {
		if _, err := Parse(withInput(t, args[0], args[1:]...)); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

// TestRollingStartStdoutKept: an explicit half on stdout is honoured rather
// than being overridden by the combined-rolling default.
func TestRollingStartStdoutKept(t *testing.T) {
	cfg, err := Parse(withInput(t, "character", "--stdout", "--rolling-end"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RollingEnd || cfg.Rolling {
		t.Errorf("explicit --rolling-end on stdout overridden: %+v", cfg)
	}
}

// TestBetaTemplate: --beta-template names its folder from the <output_file>, so
// it requires one in charset mode and is rejected without an output, with
// --stdout, or outside charset mode.
func TestBetaTemplate(t *testing.T) {
	cfg, err := Parse(withInput(t, "charset", "out", "--beta-template"))
	if err != nil {
		t.Fatalf("charset out --beta-template: %v", err)
	}
	if !cfg.BetaTemplate || cfg.Stdout || cfg.Output != "out" {
		t.Errorf("flags = %+v (want BetaTemplate set, Output=out, Stdout false)", cfg)
	}

	bad := [][]string{
		{"charset", "--beta-template"},                    // missing output
		{"charset", "out", "--beta-template", "--stdout"}, // can't combine
		{"word", "out", "--beta-template"},                // wrong mode
	}
	for _, args := range bad {
		if _, err := Parse(withInput(t, args[0], args[1:]...)); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

// TestMissingInputDetected: a non-existent input yields an IsInputMissing
// error (so main can skip the usage dump), distinct from a usage error.
func TestMissingInputDetected(t *testing.T) {
	_, err := Parse([]string{"word", filepath.Join(t.TempDir(), "nope.txt"), "--stdout"})
	if !IsInputMissing(err) {
		t.Fatalf("want IsInputMissing, got %v", err)
	}
	// A genuine usage error must not be classified as a missing input.
	if _, err := Parse([]string{"bogus"}); IsInputMissing(err) {
		t.Error("bad subcommand misclassified as missing input")
	}
}

// TestCacheDir: a valid --cache-dir is accepted; a missing one is rejected.
func TestCacheDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Parse(withInput(t, "word", "--stdout", "--cache-dir="+dir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CacheDir != dir {
		t.Errorf("CacheDir = %q, want %q", cfg.CacheDir, dir)
	}
	if _, err := Parse(withInput(t, "word", "--stdout", "--cache-dir="+filepath.Join(dir, "nope"))); err == nil {
		t.Error("expected error for non-existent --cache-dir")
	}
}

// TestMinOccurrenceRemoved: the old flag name is no longer accepted.
func TestMinOccurrenceRemoved(t *testing.T) {
	if _, err := Parse(withInput(t, "word", "out", "--min-occurrence=2")); err == nil {
		t.Error("expected --min-occurrence to be rejected as unknown")
	}
}
