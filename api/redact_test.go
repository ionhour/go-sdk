package api

import "testing"

func TestRedactTokenCap(t *testing.T) {
	cases := map[string]string{
		"ionh_live_9f8e7d6c5b4a39281706": "ionh_liv…", // capped at 8
		"abcdefghijklmnop":               "abcdefgh…", // 16 -> half is exactly 8
		"abcdefghijklmno":                "abcdefg…",  // 15 -> half is 7
		"abcdef":                         "abc…",
		"abcde":                          "ab…",
		"a":                              "…",
		"":                               "…",
		"ключ-ключ-ключ-ключ":            "ключ-клю…", // runes, not bytes
	}
	for in, want := range cases {
		if got := redactToken(in); got != want {
			t.Fatalf("redactToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubSecrets(t *testing.T) {
	if got := scrubSecrets("a KEY b KEY", "KEY"); got != "a K… b K…" {
		t.Fatalf("scrubSecrets = %q", got)
	}
	if got := scrubSecrets("nothing here", ""); got != "nothing here" {
		t.Fatalf("empty secret changed the text: %q", got)
	}
}
