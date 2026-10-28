package main

import "testing"

func TestNormalizeTokenMatchesBrowserCookieDecoding(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		" plain-token\n":                  "plain-token",
		`"quoted-token"`:                  "quoted-token",
		"abc%2Fdef%2Bghi%3D":              "abc/def+ghi=",
		"percent%2525is-decoded-once":     "percent%25is-decoded-once",
		"plus+must+not+become+whitespace": "plus+must+not+become+whitespace",
	}
	for input, want := range tests {
		if got := normalizeToken(input); got != want {
			t.Errorf("normalizeToken(%q)=%q, want %q", input, got, want)
		}
	}
}
