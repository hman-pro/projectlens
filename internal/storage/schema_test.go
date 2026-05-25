package storage

import "testing"

func TestQuoteSchema(t *testing.T) {
	cases := map[string]string{
		"ingest":     `"ingest"`,
		"projectlens": `"projectlens"`,
	}
	for in, want := range cases {
		got := QuoteSchema(in)
		if got != want {
			t.Errorf("QuoteSchema(%q)=%q want %q", in, got, want)
		}
	}
}
