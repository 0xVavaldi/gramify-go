# Gramify (Go)

Gramify is an analysis tool built with the purpose of enhancing attacks on complex passwords through the use of n-grams.
Gramify offers three types of n-gram analysis
- Word
- Character
- Charset

These each perform n-grams at their respective levels. This is a Go rewrite of the original `gramify.py` and focused on the core word/character/charset functionality with built-in results. The original extra features (`--ngram-more`, `--filter`, `--mixed`, `--cgram-rulify`, `--filter-combo`) are intentionally **not** part of this port. A deep per-function analysis of the original Python lives in [`docs/ANALYSIS.md`](docs/ANALYSIS.md).

## What is an n-gram?
Those unfamiliar with the term will most easily understand it as the n words that follow each other naturally. At a word level the sentence: "I am writing a program" can be split at 2-gram level into: `["I am", "am writing", "writing a", "a program"]`. at 3-gram level into: `["I am writing", "am writing a", "writing a program"]`. This can also be done at a character level for example with "abc defg" into the 3-gram `["abc", "bc ", "c d", " de", "def", "efg"]`. Logically you can imagine that using this on books, or song lyrics can turn into a powerful analytical form where you can extract quotes or find words commonly used together such as the words: "I am", "He is" instead of: "capricorn icecream".

## Build

```
make build
# or:
go build -o bin/gramify ./cmd/gramify
```

```
gramify word      <input_file> (<output_file>|--stdout) [--min-length=<int>] [--max-length=<int>] [--weighted]
gramify character <input_file> (<output_file>|--stdout) [--min-length=<int>] [--max-length=<int>] [--rolling] [--weighted]
gramify charset   <input_file> (<output_file>|--stdout) [--weighted]
```

## Word n-grams

```gramify word <input_file> <output_file> [--min-length=<int>] [--max-length=<int>]```
--min-length refers to the minimum amount of words that ngrams should be. Ergo. at least `<int>` words (Default: 1)
--max-length refers to the maximum amount of words that ngrams should be. Ergo. at least `<int>` words (Default: 10)

Expecting long quotes or lyrics? Increase the --max-length, the penalty is often minor.
Example of input file format:
```
But now that I'm home feels like I'm in heaven
See I been travelin'
Ooh, whoah ooh whoah
I'm in heaven
Oh and I'm, feeling right at home
Feeling right at home
Feeling like I'm in heaven
It's like I'm in heaven
It's like I'm in heaven, ooh we oh
```

Output is written to `n_<output_file>`. Unlike the original, it no longer contains raw duplicates: the output is sorted by occurrence with the most frequent grams (such as the word "It's" and "I'm" as 1-gram or "I'm in heaven" as 3-gram) at the top, so you can simply HEAD the output file if the data is too much. See [Sorting & output](#sorting--output).

Read more: https://en.wikipedia.org/wiki/N-gram. 

Some recommended commands would be:
```
gramify word <input_file> <output_file>
gramify word <input_file> <output_file> --max-length=32

hashcat -a0 -m0 <hashlist> n_<output_file> -r <sentence_mangling_rules>
hashcat -a1 -m0 <hashlist> n_<output_file> n_<output_file> -j"$ "
```

## Character n-grams (k-grams)

```gramify character <input_file> <output_file> [--min-length=<int>] [--max-length=<int>] [--rolling]```

In this port `--min-length` / `--max-length` do **not** bound the gram length — they bound **how many characters are removed** from the analysed plaintext. For a line of length `n` and each `k` between `--min-length` and `--max-length` (with `k < n`):

- **k_start** = the first `n-k` characters (the last `k` removed). Ideal with hashcat `-a6` as the input dictionary with a `k`-position mask appended to it.
- **k_end** = the last `n-k` characters (the first `k` removed). Ideal with hashcat `-a7` as the end of the word with a `k`-position mask prepended to it.

--min-length refers to the minimum number of characters removed. (Default: 1)
--max-length refers to the maximum number of characters removed. (Default: 4)

So `--max-length 4` means a 4-position hashcat mask (`?a?a?a?a`) reconstructs the analysed plaintext. The split files are written as `k_start.<output_file>` and `k_end.<output_file>` (there is no `mid_` in this port).

--rolling addresses some of the limitations this has. The benefit of having them split is that you have parts that each have a specific function. But sometimes you're not looking for the specific start/end. `--rolling` produces **one** combined file `k_rolling.<output_file>` (start ∪ end) that can easily be fed into both `-a6` and `-a7`. `--stdout` likewise produces the single combined stream.

Some recommended commands would be:
```
gramify character <input_file> <output_file>
gramify character <input_file> <output_file> --max-length=8
gramify character <input_file> <output_file> --rolling

hashcat -a6 -m0 <hashlist> k_start.<output_file> ?a?a?a?a -i
hashcat -a7 -m0 <hashlist> ?a?a?a?a k_end.<output_file> -i
hashcat -a1 -m0 <hashlist> k_start.<output_file> k_end.<output_file>
hashcat -a6 -m0 <hashlist> k_rolling.<output_file> ?a?a?a?a -i
hashcat -a7 -m0 <hashlist> ?a?a?a?a k_rolling.<output_file> -i
```
Read more: https://nlp.stanford.edu/IR-book/html/htmledition/k-gram-indexes-for-wildcard-queries-1.html

## Charset n-grams

```gramify charset <input_file> <output_file> [--normalize] [--mixed] [--min-occurrence=<int>]```

Charset mode splits cleanly on character-set differences and (apart from `--normalize` / `--min-occurrence`) takes no length parameters.
This type of n-gram is focused on character set boundaries. Moving from UPPERCASE to lowercase. From Digits to lowercase or vice versa. This way you're able to take the passwords:
```
password123456 -> [password, 123456]
PASSword123123magicman -> [PASS, word, 123123, magicman]
THEBESTINTHEWORLD54321 -> [THEBESTINTHEWORLD, 54321]
```
This can be great for extracting words or common patterns out of passwords, removing punctuation or discovering common themes. From here on we can use rules on our newly discovered words to find new passwords.

Each maximal run of a single class (lowercase letter / uppercase letter / digit / other) becomes one segment, and a class change ends the current segment. Note this is a strict, clean split: unlike the original Python there is no special "first uppercase joins the following lowercase" rule, so `PassWord123` splits into `[P, ass, W, ord, 123]`, and runs of punctuation/spaces form their own `other` segments. The original's `--mixed` and `--filter` passes are not part of this port.

Output is written to `c_<output_file>`.

### --normalize (de-leetspeak)

`--normalize` reverses [`hashcat-7.1/rules/leetspeak.rule`](docs/leetspeak.reversed.rule) and applies it to each line **before** splitting, so leet substitution characters that would otherwise sit on charset boundaries collapse back into the surrounding word:

```
p4ssw0rd -> password -> [password]
h3ll0    -> hello    -> [hello]
```

The reversed mapping is `4@→a`, `6→b`, `<{→c`, `3→e`, `9→g`, `1!→i`, `0→o`, `5$→s`, `7+→t`, `%→x`. The original rule maps both `g→9` and `q→9`; the reverse of `9` is ambiguous, so `9→g` is kept (first occurrence) and `9→q` is dropped.

Because de-leeted noise can be very low quality, `--normalize` raises the default minimum occurrence to **2** (a gram must appear at least twice to be written). Use `--min-occurrence=<int>` to fiddle with the threshold (default 1; `--min-occurrence=1` keeps everything). `--min-occurrence` is available in every mode. Count-based filtering requires aggregation, so `--normalize` / `--min-occurrence > 1` route through the frequency sorter even on `--stdout` (and cannot be combined with `--disable-cache`).

Inspired by: https://github.com/hops/pack2 (https://github.com/hops/pack2/blob/master/src/cgrams.rs)

## Sorting & output

Instead of recommending a `sort | uniq -c | sort -rn` post-processing command, Gramify now rolls its own frequency sorting. It does **not** store the full dataset in memory: keys are counted in a bounded in-memory buffer, spilled to temporary disk runs, k-way merged, and externally sorted by descending frequency. Sorting by occurrence puts the best candidates at the top, so you can HEAD the output if the data is too much.

- Default: one gram per line, most frequent first (de-duplicated).
- `--weighted`: retains the weights similar to `uniq -c` — each line is `<count>\t<gram>` (tab-separated so grams that contain spaces stay parseable), still ordered by descending frequency.

### --stdout and the dedup cache

`--stdout` streams the result instead of writing a file:

- Non-weighted `--stdout` streams in first-seen order and filters duplicates using an in-memory cache (a set of 64-bit `hash/maphash` hashes). This is bounded only by the number of distinct grams.
- `--disable-cache` (only valid with `--stdout`) drops that cache so there is no memory constraint or buffer-time — at the cost of duplicates in the output.
- `--weighted` requires aggregation, so weighted `--stdout` still uses the disk-backed frequency sorter (and cannot be combined with `--disable-cache`).

> The dedup cache is hash-based; a 64-bit hash collision would drop a distinct gram (negligible below ~1e8–1e9 distinct grams). Use `--weighted` if exact, counted output is required.

