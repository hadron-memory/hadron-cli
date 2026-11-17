package output

import (
	"strings"
	"testing"
)

func TestSafeBlock1113(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		bound          int
	}{
		{"clean", "hello\n  world", "hello\n      world", 100},
		{"escapes", "a\x1b[31mb\x1b]8;;https://evil\x07c\x1b]8;;\x1b\\d\x1bXe", "abcde", 100},
		{"controls", "a\r\n\t\r\x00\x7f\u009b\u202e\u200b\ufeff\u2028\u2029z", "a\n              z", 100},
		{"astral", "😀😀", "😀😀", 2},
		{"cut", "😀😀x", "😀😀…\n    (truncated — --json carries the whole value)", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeBlock(tc.in, tc.bound, "    "); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if got := SafeBlock(strings.Repeat("x", 20001), 20000, "    "); !strings.HasPrefix(got, strings.Repeat("x", 20000)+"…\n") {
		t.Fatal("wrong bound")
	}
}
