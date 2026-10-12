// Package normalize de-leetspeaks a string: it reverses the substitutions in
// hashcat-7.1/rules/leetspeak.rule, mapping each leet character back to its
// plain letter (see docs/leetspeak.reversed.rule).
//
// The reverse of a hashcat `sXY` rule (substitute X with Y) is Y -> X. The
// original rule maps both g->9 (sg9) and q->9 (sq9); the reverse of '9' is
// therefore ambiguous. We keep 9 -> g (the first occurrence in the rule file)
// and drop 9 -> q.
package normalize

// deleet maps a leet rune to its plain letter.
var deleet = map[rune]rune{
	'4': 'a', '@': 'a',
	'6': 'b',
	'<': 'c', '{': 'c',
	'3': 'e',
	'9': 'g', // conflict: leetspeak.rule also has sq9 (q->9); 'g' chosen.
	'1': 'i', '!': 'i',
	'0': 'o',
	'5': 's', '$': 's',
	'7': 't', '+': 't',
	'%': 'x',
}

// Apply returns s with every leet character replaced by its plain letter.
// Characters not in the table are left unchanged.
func Apply(s string) string {
	var changed bool
	for _, r := range s {
		if _, ok := deleet[r]; ok {
			changed = true
			break
		}
	}
	if !changed {
		return s
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if repl, ok := deleet[r]; ok {
			out = append(out, repl)
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}
