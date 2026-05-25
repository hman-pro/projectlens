package summaries

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/parser"
	"github.com/hman-pro/projectlens/internal/providers/identity"
)

// mockSummarizer records calls and returns canned responses.
type mockSummarizer struct {
	calls []mockCall
}

type mockCall struct {
	PackageName     string
	ExportedSymbols []string
}

func (m *mockSummarizer) GeneratePackageSummary(_ context.Context, packageName string, exportedSymbols []string) (string, error) {
	m.calls = append(m.calls, mockCall{
		PackageName:     packageName,
		ExportedSymbols: exportedSymbols,
	})
	return fmt.Sprintf("Summary of %s with %d symbols.", packageName, len(exportedSymbols)), nil
}

func (m *mockSummarizer) SummaryIdentity() identity.ProviderIdentity {
	return identity.ProviderIdentity{}
}

func TestGeneratePackageSummaries_OnlyExportedSymbols(t *testing.T) {
	mock := &mockSummarizer{}
	packages := map[string][]parser.Symbol{
		"mypkg": {
			{Name: "Exported", Kind: "func", Signature: "func Exported()"},
			{Name: "unexported", Kind: "func", Signature: "func unexported()"},
			{Name: "AnotherExported", Kind: "struct", Signature: "type AnotherExported struct{}"},
			{Name: "private", Kind: "var", Signature: "var private int"},
		},
	}

	result, err := generatePackageSummariesWith(context.Background(), mock, packages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have called the summarizer once for "mypkg".
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}

	call := mock.calls[0]
	if call.PackageName != "mypkg" {
		t.Errorf("expected package name 'mypkg', got %q", call.PackageName)
	}

	// Should only have sent exported symbols.
	if len(call.ExportedSymbols) != 2 {
		t.Fatalf("expected 2 exported symbols, got %d: %v", len(call.ExportedSymbols), call.ExportedSymbols)
	}

	for _, sym := range call.ExportedSymbols {
		if strings.Contains(sym, "unexported") || strings.Contains(sym, "private") {
			t.Errorf("unexported symbol %q should not be sent to summarizer", sym)
		}
	}

	// Check the result map has the right key.
	if _, ok := result["mypkg"]; !ok {
		t.Error("expected result to contain key 'mypkg'")
	}
}

func TestGeneratePackageSummaries_MultiplePackages(t *testing.T) {
	mock := &mockSummarizer{}
	packages := map[string][]parser.Symbol{
		"alpha": {
			{Name: "AlphaFunc", Kind: "func", Signature: "func AlphaFunc()"},
		},
		"beta": {
			{Name: "BetaType", Kind: "struct", Signature: "type BetaType struct{}"},
			{Name: "BetaFunc", Kind: "func", Signature: "func BetaFunc() error"},
		},
	}

	result, err := generatePackageSummariesWith(context.Background(), mock, packages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}

	if _, ok := result["alpha"]; !ok {
		t.Error("expected result to contain key 'alpha'")
	}
	if _, ok := result["beta"]; !ok {
		t.Error("expected result to contain key 'beta'")
	}

	// Verify correct number of calls.
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
}

func TestGeneratePackageSummaries_NoExportedSymbols(t *testing.T) {
	mock := &mockSummarizer{}
	packages := map[string][]parser.Symbol{
		"internal": {
			{Name: "helper", Kind: "func", Signature: "func helper()"},
			{Name: "doStuff", Kind: "func", Signature: "func doStuff()"},
		},
	}

	result, err := generatePackageSummariesWith(context.Background(), mock, packages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not call the summarizer when there are no exported symbols.
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 calls for package with no exports, got %d", len(mock.calls))
	}

	if summary, ok := result["internal"]; !ok {
		t.Error("expected result to contain key 'internal'")
	} else if !strings.Contains(summary, "no exported symbols") {
		t.Errorf("expected fallback message about no exports, got: %q", summary)
	}
}

func TestGeneratePackageSummaries_EmptyMap(t *testing.T) {
	mock := &mockSummarizer{}
	packages := map[string][]parser.Symbol{}

	result, err := generatePackageSummariesWith(context.Background(), mock, packages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected empty result for empty input, got %d entries", len(result))
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 calls for empty input, got %d", len(mock.calls))
	}
}

func TestGeneratePackageSummaries_UsesSignatureField(t *testing.T) {
	mock := &mockSummarizer{}
	packages := map[string][]parser.Symbol{
		"pkg": {
			{Name: "Foo", Kind: "func", Signature: "func Foo(x int) error"},
			{Name: "Bar", Kind: "struct", Signature: ""}, // no signature, should fall back
		},
	}

	_, err := generatePackageSummariesWith(context.Background(), mock, packages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}

	syms := mock.calls[0].ExportedSymbols
	if len(syms) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(syms))
	}

	if syms[0] != "func Foo(x int) error" {
		t.Errorf("expected first symbol to be signature, got %q", syms[0])
	}
	// When signature is empty, should fall back to "kind name".
	if syms[1] != "struct Bar" {
		t.Errorf("expected fallback 'struct Bar' for empty signature, got %q", syms[1])
	}
}

// errorSummarizer always returns an error.
type errorSummarizer struct{}

func (e *errorSummarizer) GeneratePackageSummary(_ context.Context, packageName string, _ []string) (string, error) {
	return "", fmt.Errorf("API error for %s", packageName)
}

func (e *errorSummarizer) SummaryIdentity() identity.ProviderIdentity {
	return identity.ProviderIdentity{}
}

func TestGeneratePackageSummaries_PropagatesError(t *testing.T) {
	packages := map[string][]parser.Symbol{
		"failing": {
			{Name: "X", Kind: "func", Signature: "func X()"},
		},
	}

	_, err := generatePackageSummariesWith(context.Background(), &errorSummarizer{}, packages)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "API error") {
		t.Errorf("expected error to contain 'API error', got: %v", err)
	}
}

func TestExportedSignatures_FiltersCorrectly(t *testing.T) {
	symbols := []parser.Symbol{
		{Name: "Public", Kind: "func", Signature: "func Public()"},
		{Name: "private", Kind: "func", Signature: "func private()"},
		{Name: "Another", Kind: "struct", Signature: "type Another struct{}"},
		{Name: "_underscore", Kind: "var", Signature: "var _underscore int"},
	}

	sigs := exportedSignatures(symbols)

	if len(sigs) != 2 {
		t.Fatalf("expected 2 exported signatures, got %d: %v", len(sigs), sigs)
	}

	if sigs[0] != "func Public()" {
		t.Errorf("expected 'func Public()', got %q", sigs[0])
	}
	if sigs[1] != "type Another struct{}" {
		t.Errorf("expected 'type Another struct{}', got %q", sigs[1])
	}
}
