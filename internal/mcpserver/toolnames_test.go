package mcpserver

import (
	"slices"
	"testing"
)

func TestToolNames(t *testing.T) {
	names := ToolNames()
	if len(names) == 0 {
		t.Fatal("no tool names")
	}
	if !slices.IsSorted(names) {
		t.Errorf("not sorted: %v", names)
	}
	for _, want := range []string{"find_symbol", "index_status"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing %q in %v", want, names)
		}
	}
}
