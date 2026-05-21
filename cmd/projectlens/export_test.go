package main

import "testing"

func TestParseEdges_All(t *testing.T) {
	got, err := parseEdges("all")
	if err != nil || got != nil {
		t.Errorf("all: got (%v,%v)", got, err)
	}
	got, err = parseEdges("")
	if err != nil || got != nil {
		t.Errorf("empty: got (%v,%v)", got, err)
	}
}

func TestParseEdges_Valid(t *testing.T) {
	got, err := parseEdges("calls,reads_table")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 || got[0] != "calls" || got[1] != "reads_table" {
		t.Errorf("got %v", got)
	}
}

func TestParseEdges_Invalid(t *testing.T) {
	if _, err := parseEdges("calls,bogus"); err == nil {
		t.Errorf("want error for bogus")
	}
}
