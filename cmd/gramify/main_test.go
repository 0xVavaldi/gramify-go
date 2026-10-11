package main

import "testing"

// TestSanitizeName: the template folder name is derived from the output's base
// name with its final extension stripped, unsafe characters folded to '_', and
// traversal/dot results neutralised. Both '/' and '\' are handled on every OS.
func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"rockyou.txt":             "rockyou",      // extension dropped
		"rockyou":                 "rockyou",      // no extension
		"list.dict.txt":           "list.dict",    // only the final extension
		"sub/dir/words.txt":       "words",        // directory components dropped
		"../../etc/passwd":        "passwd",       // no traversal
		`..\..\etc\passwd`:        "passwd",       // backslash separators (any OS)
		`C:\Users\me\rockyou.txt`: "rockyou",      // Windows absolute path
		`sub\dir/words.txt`:       "words",        // mixed separators
		"we ird@name!.txt":        "we_ird_name_", // unsafe chars folded
		"":                        "input",
		".":                       "input",
		"..":                      "input",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
