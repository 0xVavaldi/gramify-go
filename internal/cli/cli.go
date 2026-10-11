// Package cli parses and validates command-line arguments into a typed Config.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Vavaldi/gramify/internal/membudget"
)

// Mode selects which generator runs: word (n-gram), character (k-gram), or
// charset (c-gram).
type Mode int

// The supported subcommands, in declaration order.
const (
	ModeWord      Mode = iota // word: contiguous word-window n-grams
	ModeCharacter             // character: prefix/suffix k-grams
	ModeCharset               // charset: maximal same-class segments
)

// Config is the fully validated, defaults-applied result of parsing the
// command line. It is the single input that drives a run.
type Config struct {
	Mode      Mode
	Input     string
	Output    string // ignored when Stdout is true
	Stdout    bool
	MinLength int
	MaxLength int
	Weighted  bool

	// Rolling, RollingStart and RollingEnd (character mode only) each select a
	// single-stream shape instead of split k_start./k_end. files. Rolling emits
	// the combined start∪end stream (usable for both -a6 and -a7); RollingStart
	// emits only prefixes (-a6); RollingEnd emits only suffixes (-a7). At most
	// one may be set. Rolling is auto-enabled for --stdout when none is given.
	Rolling      bool
	RollingStart bool
	RollingEnd   bool

	// Normalize (charset mode only) de-leetspeaks each line before splitting.
	Normalize bool

	// Purist (charset mode only) reverts to strict per-class splitting: upper
	// and lowercase are distinct classes, every class change cuts, and there is
	// no insertion gluing or camelCase decomposition.
	Purist bool

	// SubLength (charset mode only) is the substitution/insertion threshold: a
	// middle run of <= SubLength runes glues its neighbours into one segment
	// instead of splitting. Default 1, max 2; ignored (forced 0) when Purist.
	SubLength int

	// BetaTemplate (charset mode only) writes a folder of positional part files
	// (c_<n>_<id>.txt) plus c_start_<k>/c_end_<k> bundles instead of a single
	// output. It requires an <output_file> (the folder is c_<output>_templates,
	// sanitised) and is incompatible with --stdout.
	BetaTemplate bool

	// CaseSensitive (charset mode only) disables the default case-insensitive
	// frequency grouping, restoring exact-case counting in the freq-sorted
	// output. The other modes are always case-sensitive.
	CaseSensitive bool

	// MinFreq drops grams occurring fewer than this many times. It requires
	// aggregation, so it applies to file output only, defaulting to 2 (drop
	// singletons). On --stdout it is forced to 1 (no filtering) and an explicit
	// --min-freq>1 (or --weighted) is refused (see ErrStdoutAggregation).
	MinFreq int

	// DisableCache, valid only with --stdout, removes the in-memory dedup
	// cache so output streams with no memory constraint (and with duplicates).
	DisableCache bool

	// MemoryMax is the soft memory ceiling in bytes. 0 means "derive the
	// default" (a quarter of system RAM). It is applied as GOMEMLIMIT and used
	// to size read-ahead buffers and in-memory dedup/sort state.
	MemoryMax int64

	// Debug enables memory diagnostics on stderr (the budget and the peak RAM
	// used).
	Debug bool

	// CacheDir is the parent directory for on-disk spill files. Empty means the
	// system temp dir (os.TempDir()).
	CacheDir string

	// CPUProfile / MemProfile, when non-empty, write a pprof CPU / heap profile
	// to the named file (debugging aid; undocumented).
	CPUProfile string
	MemProfile string
}

// Version is the tool version, shown by --version.
const Version = "2.0-alpha.01"

// Usage is the help text shown for -h/--help and on a parse error.
const Usage = `gramify - n-gram / k-gram / c-gram generator (Go)

Usage:
  gramify word      <input_file> [<output_file>|--stdout] [--min-length=N] [--max-length=N] [--min-freq=N] [--weighted]
  gramify character <input_file> [<output_file>|--stdout] [--min-length=N] [--max-length=N] [--min-freq=N] [--rolling|--rolling-start|--rolling-end] [--weighted]
  gramify charset   <input_file> [<output_file>|--stdout] [--normalize] [--purist] [--substitution-length=N] [--case-sensitive] [--min-length=N] [--min-freq=N] [--weighted]
  gramify charset   <input_file> <output_file> --beta-template [--normalize] [--purist] [--substitution-length=N] [--case-sensitive] [--min-freq=N] [--weighted]
  gramify (-h | --help)

Options:
  --min-length=N   word: min words per gram (default 1).
                   character: min characters removed (default 1).
                   charset: min segment length in characters (default 1); drops
                   shorter segments to skip rapidly-toggling/inserted fragments.
  --max-length=N   word: max words per gram (default 10).
                   character: max characters removed (default 4).
                   In character mode --max-length 4 means a 4-position hashcat
                   mask (?a?a?a?a) reconstructs the analysed plaintext.
  --rolling        character only: emit one combined stream (start + end)
                   usable for both -a6 and -a7 instead of split files.
                   Enabled automatically with --stdout (it can't be split).
  --rolling-start  character only: emit a single stream of prefixes only
                   (for hashcat -a6). Like --rolling but the start half alone.
  --rolling-end    character only: emit a single stream of suffixes only
                   (for hashcat -a7). Like --rolling but the end half alone.
  --normalize      charset only: de-leetspeak each line before splitting
                   (reverses hashcat leetspeak.rule).
  --purist         charset only: strict per-class splitting. Treat uppercase and
                   lowercase as separate classes and split on every class change
                   (no insertion gluing, no camelCase sub-words). The default
                   instead folds case, glues short inserted/substituted runs, and
                   also emits camelCase sub-words (>=4 chars) alongside the whole.
  --substitution-length=N
                   charset only: max length (in characters) of a MIDDLE run
                   treated as an inserted/leetspeak substitution that glues its
                   neighbours into one segment instead of splitting (default 1,
                   max 2; e.g. "A1two" stays whole). Runs at the start/end always
                   split. Ignored with --purist.
  --beta-template  charset only: instead of one output file, write a folder
                   c_<output>_templates/ where each line's n charset parts go to
                   c_<n>_<id>.txt (id 1..n; e.g. "P0tato123!" -> P0tato/123/!).
                   It also writes merged prefix/suffix bundles: c_start_<k>.txt
                   holds the first k parts joined and c_end_<k>.txt the last k,
                   for every width k from 1 to n-1 (k stays below n). Each file
                   is its own dedup+freq-sorted output. Parts are never
                   length-filtered (--min-length does not apply). Cannot be
                   combined with --stdout or an <output_file>.
  --case-sensitive charset only: keep exact-case frequency counting. By default
                   charset freq-sorted output is case-insensitive: case variants
                   are summed and the most-frequent casing is emitted.
  --min-freq=N     drop grams occurring fewer than N times (default 2).
                   File output only; an explicit value with --stdout is refused.
  --weighted       prefix each output line with its frequency ("<count>\t<gram>",
                   like uniq -c). Default output is gram-only, most frequent first.
  --stdout         write the result to stdout (the default when no
                   <output_file> is given). Output is the exact de-duplicated
                   set in key order; it is computed with a memory-bounded,
                   disk-backed merge, so it stays accurate even when the data
                   far exceeds --memory-max.
  --disable-cache  only with --stdout: stream keys straight through in arrival
                   order with no de-duplication and no memory use (output will
                   contain duplicates).
  --memory-max=SIZE
                   soft ceiling on memory use (e.g. 512M, 2G; suffixes B/K/M/G,
                   default a quarter of system RAM).
  --cache-dir=DIR  directory for on-disk spill files when data exceeds
                   --memory-max (default: system temp dir). Removed on exit.
  --debug          print memory diagnostics to stderr (the budget and the peak
                   RAM used).
  --version        print the version and exit.
  -h --help        show this help.

If neither <output_file> nor --stdout is given, output goes to stdout.

File output is sorted by descending frequency; --weighted additionally prefixes
each line with its count. --stdout output is always bare grams: the exact
de-duplicated set in key order, or raw with duplicates when --disable-cache is
set.`

// seen tracks which integer options the user set explicitly, so defaults are
// only applied when the option was omitted.
type seen struct{ min, max, minFreq, subLen bool }

// Parse builds a validated Config from the argument list. It is split into
// focused helpers (parseMode, scanArgs, applyPositionals, validate) to keep
// each under ~60 lines (NASA P10 rule 4).
//
// Args:
//
//	args: the program arguments excluding the program name (i.e. os.Args[1:]).
//
// Returns:
//
//	the validated Config, or an error. The error is one of the sentinels
//	errHelp, ErrStdoutAggregation, or ErrInputMissing (test with IsHelp /
//	IsStdoutAggregation / IsInputMissing), or a plain validation error.
func Parse(args []string) (*Config, error) {
	for _, a := range args { // bounded by len(args) (rule 2)
		if a == "-h" || a == "--help" {
			return nil, errHelp
		}
		if a == "--version" {
			return nil, errVersion
		}
	}
	mode, err := parseMode(args)
	if err != nil {
		return nil, err
	}

	c := &Config{Mode: mode}
	positionals, set, err := scanArgs(c, args)
	if err != nil {
		return nil, err
	}
	if err := applyPositionals(c, positionals); err != nil {
		return nil, err
	}
	if err := validate(c, set); err != nil {
		return nil, err
	}
	return c, nil
}

// parseMode resolves the subcommand in args[0].
//
// Args:
//
//	args: the full argument slice.
//
// Returns:
//
//	the selected Mode, or an error if the subcommand is missing/unknown.
func parseMode(args []string) (Mode, error) {
	if len(args) < 1 {
		return 0, errors.New("missing subcommand (word|character|charset)")
	}
	switch args[0] {
	case "word":
		return ModeWord, nil
	case "character":
		return ModeCharacter, nil
	case "charset":
		return ModeCharset, nil
	default:
		return 0, fmt.Errorf("unknown subcommand %q (want word|character|charset)", args[0])
	}
}

// scanArgs walks args[1:], setting boolean/value flags on c and collecting
// positionals.
//
// Args:
//
//	c:    the Config to populate (mutated in place).
//	args: the full argument slice.
//
// Returns:
//
//	the positional arguments, a `seen` record of explicitly-set integer
//	options, and an error for malformed/unknown options.
func scanArgs(c *Config, args []string) ([]string, seen, error) {
	var positionals []string
	var set seen
	intFlag := func(i int, name string, dst *int, flag *bool) (int, error) {
		v, ni, err := flagValue(args, i, name)
		if err != nil {
			return i, err
		}
		if *dst, err = atoi(v, name); err != nil {
			return i, err
		}
		*flag = true
		return ni, nil
	}

	for i := 1; i < len(args); i++ { // bounded by len(args) (rule 2)
		a := args[i]
		var err error
		switch {
		case a == "--stdout":
			c.Stdout = true
		case a == "--weighted":
			c.Weighted = true
		case a == "--rolling":
			c.Rolling = true
		case a == "--rolling-start":
			c.RollingStart = true
		case a == "--rolling-end":
			c.RollingEnd = true
		case a == "--disable-cache":
			c.DisableCache = true
		case a == "--debug":
			c.Debug = true
		case strings.HasPrefix(a, "--cpuprofile"):
			v, ni, ferr := flagValue(args, i, "--cpuprofile")
			if ferr != nil {
				err = ferr
			} else {
				c.CPUProfile, i = v, ni
			}
		case strings.HasPrefix(a, "--memprofile"):
			v, ni, ferr := flagValue(args, i, "--memprofile")
			if ferr != nil {
				err = ferr
			} else {
				c.MemProfile, i = v, ni
			}
		case strings.HasPrefix(a, "--cache-dir"):
			v, ni, ferr := flagValue(args, i, "--cache-dir")
			if ferr != nil {
				err = ferr
			} else {
				c.CacheDir = v
				i = ni
			}
		case strings.HasPrefix(a, "--memory-max"):
			v, ni, ferr := flagValue(args, i, "--memory-max")
			if ferr != nil {
				err = ferr
			} else if c.MemoryMax, err = membudget.ParseSize(v); err == nil {
				i = ni
			}
		case a == "--normalize":
			c.Normalize = true
		case a == "--purist":
			c.Purist = true
		case a == "--beta-template":
			c.BetaTemplate = true
		case a == "--case-sensitive":
			c.CaseSensitive = true
		case strings.HasPrefix(a, "--substitution-length"):
			i, err = intFlag(i, "--substitution-length", &c.SubLength, &set.subLen)
		case strings.HasPrefix(a, "--min-freq"):
			i, err = intFlag(i, "--min-freq", &c.MinFreq, &set.minFreq)
		case strings.HasPrefix(a, "--min-length"):
			i, err = intFlag(i, "--min-length", &c.MinLength, &set.min)
		case strings.HasPrefix(a, "--max-length"):
			i, err = intFlag(i, "--max-length", &c.MaxLength, &set.max)
		case strings.HasPrefix(a, "-"):
			err = fmt.Errorf("unknown option %q", a)
		default:
			positionals = append(positionals, a)
		}
		if err != nil {
			return nil, set, err
		}
	}
	return positionals, set, nil
}

// applyPositionals assigns the input/output positionals onto c.
//
// Args:
//
//	c:           the Config to populate.
//	positionals: collected non-flag arguments.
//
// Returns:
//
//	an error if the count is wrong or input/--stdout are inconsistent.
func applyPositionals(c *Config, positionals []string) error {
	if len(positionals) < 1 {
		return errors.New("missing <input_file>")
	}
	if len(positionals) > 2 {
		return fmt.Errorf("unexpected extra argument %q", positionals[2])
	}
	c.Input = positionals[0]
	if len(positionals) == 2 {
		c.Output = positionals[1]
	}
	if c.Stdout && c.Output != "" {
		return errors.New("provide either <output_file> or --stdout, not both")
	}
	if !c.Stdout && c.Output == "" && !c.BetaTemplate {
		c.Stdout = true // default: stream to stdout when neither is given
	}
	return nil
}

// validate applies defaults and enforces every flag-combination and range
// rule, finally checking that the input file exists.
//
// Args:
//
//	c:   the populated Config.
//	set: which integer options the user set explicitly.
//
// Returns:
//
//	the first validation error encountered, or nil if the Config is valid.
func validate(c *Config, set seen) error {
	if (c.Rolling || c.RollingStart || c.RollingEnd) && c.Mode != ModeCharacter {
		return errors.New("--rolling/--rolling-start/--rolling-end are only valid in character mode")
	}
	// At most one rolling shape may be selected.
	if c.RollingStart && c.RollingEnd {
		return errors.New("--rolling-start and --rolling-end are mutually exclusive")
	}
	if c.Rolling && (c.RollingStart || c.RollingEnd) {
		return errors.New("--rolling cannot be combined with --rolling-start/--rolling-end")
	}
	// stdout cannot be split into two files, so character mode emits a single
	// stream there: honour an explicit half, else default to combined rolling.
	if c.Mode == ModeCharacter && c.Stdout && !c.Rolling && !c.RollingStart && !c.RollingEnd {
		c.Rolling = true
	}
	if c.DisableCache && !c.Stdout {
		return errors.New("--disable-cache is only valid with --stdout")
	}
	if c.Normalize && c.Mode != ModeCharset {
		return errors.New("--normalize is only valid in charset mode")
	}
	if c.Purist && c.Mode != ModeCharset {
		return errors.New("--purist is only valid in charset mode")
	}
	if set.subLen && c.Mode != ModeCharset {
		return errors.New("--substitution-length is only valid in charset mode")
	}
	if c.Purist && set.subLen {
		return errors.New("--substitution-length has no effect with --purist")
	}
	if c.CaseSensitive && c.Mode != ModeCharset {
		return errors.New("--case-sensitive is only valid in charset mode (other modes are always case-sensitive)")
	}
	if c.BetaTemplate {
		if c.Mode != ModeCharset {
			return errors.New("--beta-template is only valid in charset mode")
		}
		if c.Stdout {
			return errors.New("--beta-template writes a folder of files and cannot be combined with --stdout")
		}
		if c.Output == "" {
			return errors.New("--beta-template requires an <output_file> to name the template folder (c_<output>_templates)")
		}
	}

	// An explicit --min-freq must be positive.
	if set.minFreq && c.MinFreq < 1 {
		return errors.New("--min-freq must be >= 1")
	}
	// Frequency filtering (--min-freq) and counted output (--weighted) need full
	// aggregation, which only file output does. On the streaming stdout path it
	// would be inaccurate, so an explicit attempt is refused; otherwise stdout
	// simply does no filtering. File output defaults to dropping singletons.
	if c.Stdout {
		if c.Weighted || (set.minFreq && c.MinFreq > 1) {
			return ErrStdoutAggregation
		}
		c.MinFreq = 1
	} else if !set.minFreq {
		c.MinFreq = 2
	}

	if c.Mode == ModeCharset {
		if set.max {
			return errors.New("charset mode takes no --max-length")
		}
		if !set.min {
			c.MinLength = 1 // default: keep all segments
		}
		if c.MinLength < 1 {
			return errors.New("--min-length must be >= 1")
		}
		if !set.subLen {
			c.SubLength = 1 // default: glue single-char middle insertions
		}
		if !c.Purist && (c.SubLength < 1 || c.SubLength > 2) {
			return errors.New("--substitution-length must be 1 or 2")
		}
	} else {
		if !set.min {
			c.MinLength = 1
		}
		if !set.max {
			c.MaxLength = 10
			if c.Mode == ModeCharacter {
				c.MaxLength = 4
			}
		}
		if c.MinLength < 1 {
			return errors.New("--min-length must be >= 1")
		}
		if c.MaxLength < c.MinLength {
			return errors.New("--max-length must be >= --min-length")
		}
	}

	if c.CacheDir != "" {
		if st, err := os.Stat(c.CacheDir); err != nil || !st.IsDir() {
			return fmt.Errorf("--cache-dir %q is not a directory", c.CacheDir)
		}
	}

	if st, err := os.Stat(c.Input); err != nil || st.IsDir() {
		return fmt.Errorf("%w: %q", ErrInputMissing, c.Input)
	}
	return nil
}

var errHelp = errors.New("help requested")

// errVersion is the sentinel returned for --version.
var errVersion = errors.New("version requested")

// IsVersion reports whether err is the sentinel returned for --version.
func IsVersion(err error) bool { return err == errVersion }

// ErrStdoutAggregation is returned when --weighted or --min-freq>1 is combined
// with --stdout. Its message is shown to the user verbatim (without the usage
// dump) before the program exits.
var ErrStdoutAggregation = errors.New("freq sorting is partially functional for stdout and " +
	"inaccurate. --weighted and --min-freq require file output and cannot be combined with stdout")

// IsStdoutAggregation reports whether err is ErrStdoutAggregation.
func IsStdoutAggregation(err error) bool { return err == ErrStdoutAggregation }

// ErrInputMissing wraps the error returned when the input file does not exist
// (or is a directory). It is shown without the usage dump, since usage won't
// help a wrong path.
var ErrInputMissing = errors.New("input file does not exist")

// IsInputMissing reports whether err stems from a missing input file.
func IsInputMissing(err error) bool { return errors.Is(err, ErrInputMissing) }

// IsHelp reports whether err is the sentinel returned for -h/--help.
func IsHelp(err error) bool { return err == errHelp }

// flagValue extracts the value of a "--name=value" or "--name value" option.
//
// Args:
//
//	args: the full argument slice.
//	i:    index of the option token.
//	name: option name, used in the error message.
//
// Returns:
//
//	the value, the index of the last token consumed (i, or i+1 for the
//	space-separated form), and an error if the value is missing.
func flagValue(args []string, i int, name string) (string, int, error) {
	a := args[i]
	if eq := strings.IndexByte(a, '='); eq >= 0 {
		return a[eq+1:], i, nil
	}
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("%s requires a value", name)
	}
	return args[i+1], i + 1, nil
}

// atoi parses s as an integer, attributing failures to the named option.
//
// Args:
//
//	s:    the string to parse.
//	name: option name, used in the error message.
//
// Returns:
//
//	the parsed integer, or an error if s is not a valid integer.
func atoi(s, name string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", name, s)
	}
	return n, nil
}
