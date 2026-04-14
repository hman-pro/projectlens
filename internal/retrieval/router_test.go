package retrieval

import "testing"

func TestClassifyQuery_ExactSymbol(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"ReserveInventory", ExactSymbol},
		{"Handler", ExactSymbol},
		{"NewRouter", ExactSymbol},
		{"DB", ExactSymbol},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_ImplementationSearch(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"how does inventory reservation work", ImplementationSearch},
		{"find handler for requests", ImplementationSearch},
		{"search for error handling", ImplementationSearch},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_PackageOverview(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"what does pkg/temporal do", PackageOverview},
		{"package storage", PackageOverview},
		{"service/graphql", PackageOverview},
		{"internal/retrieval", PackageOverview},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_DependencyTrace(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"what calls ProcessPayment", DependencyTrace},
		{"callers of HandleRequest", DependencyTrace},
		{"depends on DatabaseService", DependencyTrace},
		{"what uses Logger", DependencyTrace},
		{"who calls ProcessOrder", DependencyTrace},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_EdgeCases(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		// Empty string defaults to implementation search.
		{"", ImplementationSearch},
		// Lowercase single word is not a Go symbol (no uppercase start).
		{"handler", ImplementationSearch},
		// Single lowercase word.
		{"foo", ImplementationSearch},
		// Number prefix.
		{"123abc", ImplementationSearch},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}
