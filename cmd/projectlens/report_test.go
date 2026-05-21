package main

import "testing"

func TestResolveFormat_ExplicitWins(t *testing.T) {
	f, err := resolveFormat("json", "report.md")
	if err != nil || f != "json" {
		t.Errorf("got (%q,%v) want (json,nil)", f, err)
	}
}

func TestResolveFormat_InferFromExtension(t *testing.T) {
	cases := map[string]string{
		"report.md":       "markdown",
		"report.markdown": "markdown",
		"report.json":     "json",
	}
	for in, want := range cases {
		f, err := resolveFormat("", in)
		if err != nil || f != want {
			t.Errorf("%s: got (%q,%v) want (%q,nil)", in, f, err, want)
		}
	}
}

func TestResolveFormat_DefaultMarkdownWithoutOut(t *testing.T) {
	f, err := resolveFormat("", "")
	if err != nil || f != "markdown" {
		t.Errorf("got (%q,%v) want (markdown,nil)", f, err)
	}
}

func TestResolveFormat_UnknownExtensionErrors(t *testing.T) {
	if _, err := resolveFormat("", "report.txt"); err == nil {
		t.Errorf("want error for .txt")
	}
}

func TestResolveFormat_InvalidFormatErrors(t *testing.T) {
	if _, err := resolveFormat("html", ""); err == nil {
		t.Errorf("want error for html")
	}
}
