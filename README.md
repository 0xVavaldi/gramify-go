# Gramify (Go)

Gramify is an analysis tool built with the purpose of enhancing attacks on complex passwords through the use of n-grams.
Gramify offers three types of 'gram' analysis.
- Word
- Character
- Character sets

In the computer science field people learn about n-grams which places a correlation on words that appear together frequently. For example: "I am doing well" consists of 4 words: ["I", "am", "doing", "well"]. This 4 word n-gram can be split into 2 word n-grams as well: ["I am", "am doing", "doing well"] correlating the 2 words that appear beside eachother until they hit either the start ("I") or the end ("well"). This tool not only highlights the correlation between words, but also between characters or character-set changes. `applePOTATO123` for example could be split into `["apple, "POTATO", "123"]`.
These each perform n-grams at their respective levels. This is a Go rewrite of the original `gramify.py` and focused on the core word/character/charset functionality with built-in results.

## What is an n-gram?
Those unfamiliar with the term will most easily understand it as the n words that follow each other naturally. At a 'word level' the sentence: "I am writing a program" can be split at 2-gram level into: `["I am", "am writing", "writing a", "a program"]`. at 3-gram level into: `["I am writing", "am writing a", "writing a program"]`. This can also be done at a character level for example with "abc defg" into the 3-gram `["abc", "bc ", "c d", " de", "def", "efg"]`. Logically you can imagine that using this on books, or song lyrics can turn into a powerful analytical form where you can extract quotes or find words commonly used together such as the words: "I am", "He is" instead of: "capricorn icecream". At scale this can be used to find the most frequently used word-combinations in thousands of books, movies, or song lyrics.

## Quick start / build

```
cd go_rewrite
make build
# or:
go build -o bin/gramify ./cmd/gramify
```

```
gramify word      <input_file> (<output_file>|--stdout) [--min-length=<int>] [--max-length=<int>] [--weighted]
gramify character <input_file> (<output_file>|--stdout) [--min-length=<int>] [--max-length=<int>] [--rolling] [--weighted]
gramify charset   <input_file> (<output_file>|--stdout) [--weighted]
```

## Build with Docker

Cross-compile both the Linux and Windows binaries in containers — no Go toolchain or matching OS needed on the host. A single `docker buildx bake` reads `docker-bake.hcl` and runs both `Dockerfile.linux` and `Dockerfile.windows` in parallel, writing the binaries straight to `bin/` on the host:

```
cd go_rewrite
docker buildx bake
# produces ./bin/gramify       (Linux  amd64)
#          ./bin/gramify.exe   (Windows amd64)
```

Or via the Makefile (same thing):

```
make docker            # both targets
make docker-linux      # Linux only
make docker-windows    # Windows only
```

Or per-Dockerfile directly:

```
docker buildx build -f Dockerfile.linux   --output ./bin .
docker buildx build -f Dockerfile.windows --output ./bin .
```

## Word n-grams

```gramify word <input_file> <output_file> [--min-length=<int>] [--max-length=<int>]```
--min-length refers to the minimum amount of words that ngrams should be.
--max-length refers to the maximum amount of words that ngrams should be.

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
Output is written to `n_<output_file>`. The output is sorted by occurrence with the most frequent combinations.
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

In this port `--min-length` / `--max-length` do **not** bound the length — they bound how many characters are **removed** to be used with hashcat -a6 and -a7. It essentially removes the first x and last x characters at the same time. For variations of --rolling-start or --rolling-end it will only move the start/end respectively.
--min-length refers to the minimum number of characters removed. (Default: 1)
--max-length refers to the maximum number of characters removed. (Default: 4)
`--max-length 4` means a 4-position hashcat mask (`?a?a?a?a` with -i) reconstructs the analysed plaintexts. The split files are written as `k_start.<output_file>` and `k_end.<output_file>` (there is no `mid_` in this port).

--rolling addresses some of the limitations this has. The benefit of having them split is that you have parts that each have a specific function. But sometimes you're not looking for the specific start/end. `--rolling` produces **one** combined file `k_rolling.<output_file>` (start ∪ end) that can easily be fed into both `-a6` and `-a7`. `--stdout` likewise produces the single combined stream. Users frequently prefix or suffix information, so removing the first/last X characters similar to `[[[[` or `]]]]` can have great value if the algorithm is relatively quick to compute (unsalted).

Some recommended commands would be:
```
gramify character <input_file> <output_file>
gramify character <input_file> <output_file> --rolling

hashcat -a6 -m0 <hashlist> k_start.<output_file> -1 ?d?s ?1?1?1?1?1?1 -i
hashcat -a7 -m0 <hashlist> -1 ?d?s ?1?1?1?1?1 k_end.<output_file> -i
hashcat -a1 -m0 <hashlist> k_start.<output_file> k_end.<output_file>
hashcat -a6 -m0 <hashlist> k_rolling.<output_file> -1 ?d?s ?1?1?1?1?1?1 -i
hashcat -a7 -m0 <hashlist> -1 ?d?s ?1?1?1?1?1 k_rolling.<output_file> -i
```
Read more: https://nlp.stanford.edu/IR-book/html/htmledition/k-gram-indexes-for-wildcard-queries-1.html

## Charset n-grams

```gramify charset <input_file> <output_file> [--normalize] [--min-occurrence=<int>]```

Charset mode splits cleanly on character-set differences and takes no length parameters. This type of n-gram is focused on character set boundaries. Moving from UPPERCASE to lowercase. From Digits to lowercase or vice versa. This is what we percieve as the most common neurological way to construct passwords and allows you to take passwords and deconstruct them. For example:
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

