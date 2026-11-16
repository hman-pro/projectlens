package mcpserver

import "sort"

// ToolNames returns the names of every tool the server exposes, sorted. It
// reads the registry only; no database, router, or listener is involved.
func ToolNames() []string {
	reg := New(nil, nil, 0, "").toolRegistry()
	names := make([]string, 0, len(reg))
	for _, r := range reg {
		names = append(names, r.tool.Name)
	}
	sort.Strings(names)
	return names
}
