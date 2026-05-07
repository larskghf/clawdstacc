package clawd

import "testing"

func TestFmtAgo(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0s"},
		{59, "59s"},
		{60, "1m"},
		{3599, "59m"},
		{3600, "1h"},
		{86399, "23h"},
		{86400, "1d"},
		{604800, "7d"},
		{-1, "?"},
	}
	for _, c := range cases {
		if got := fmtAgo(c.in); got != c.want {
			t.Errorf("fmtAgo(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", `'plain'`},
		{"with space", `'with space'`},
		{"has'quote", `'has'\''quote'`},
		{"", `''`},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
