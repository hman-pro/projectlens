package storage

import (
	"strings"
	"testing"
)

func TestKnowledgeListFiltersValidate(t *testing.T) {
	cases := []struct {
		name    string
		f       KnowledgeListFilters
		wantErr string
	}{
		{"bad category", KnowledgeListFilters{Category: "rant"}, "category"},
		{"empty ok", KnowledgeListFilters{}, ""},
		{"valid category", KnowledgeListFilters{Category: "lesson"}, ""},
		{"negative limit", KnowledgeListFilters{Limit: -1}, "limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.f.Validate()
			if (tc.wantErr == "") != (err == nil) {
				t.Fatalf("wantErr=%q got=%v", tc.wantErr, err)
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q in %v", tc.wantErr, err)
			}
		})
	}
}

func TestKnowledgeEntryValidate(t *testing.T) {
	cases := []struct {
		name    string
		entry   KnowledgeEntry
		wantErr string
	}{
		{"empty title", KnowledgeEntry{Category: "lesson", Body: "x"}, "title required"},
		{"empty body", KnowledgeEntry{Category: "lesson", Title: "x"}, "body required"},
		{"bad category", KnowledgeEntry{Category: "rant", Title: "x", Body: "y"}, "category"},
		{"valid", KnowledgeEntry{Category: "lesson", Title: "x", Body: "y"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.entry.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
