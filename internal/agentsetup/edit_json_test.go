package agentsetup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "f")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readTestFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeDoc(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return m
}

func canonicalHooks(t *testing.T) []Hook {
	t.Helper()
	hooks, err := loadClaudeHooks("../../agent/claude/settings-snippet.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 4 {
		t.Fatalf("want 4 canonical hooks, got %d", len(hooks))
	}
	return hooks
}

func TestMCPJSON_CreateReadRemove(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", ".mcp.json")
	if h, present, err := ReadMCPJSON(p); err != nil || present || h != "" {
		t.Fatalf("missing file: %q %v %v", h, present, err)
	}
	const url = "http://localhost:8080/mcp"
	if err := WriteMCPJSON(p, url); err != nil {
		t.Fatal(err)
	}
	h, present, err := ReadMCPJSON(p)
	if err != nil || !present || h != MCPHash(url) {
		t.Fatalf("read: %q %v %v", h, present, err)
	}
	raw := readTestFile(t, p)
	if !strings.HasSuffix(raw, "\n") || !strings.Contains(raw, "  \"mcpServers\"") {
		t.Errorf("want 2-space indent and trailing newline, got %q", raw)
	}
	empty, err := RemoveMCPJSON(p)
	if err != nil || !empty {
		t.Fatalf("remove: empty=%v err=%v", empty, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("remover must leave the file in place: %v", err)
	}
}

func TestMCPJSON_PreservesOtherServers(t *testing.T) {
	start := `{"mcpServers":{"other":{"command":"x","args":["a"]}},"extra":[1,2]}`
	p := writeTestFile(t, start)
	if err := WriteMCPJSON(p, "http://u/mcp"); err != nil {
		t.Fatal(err)
	}
	got := decodeDoc(t, readTestFile(t, p))
	want := decodeDoc(t, start)
	if !reflect.DeepEqual(got["extra"], want["extra"]) {
		t.Errorf("extra changed: %v", got["extra"])
	}
	servers := got["mcpServers"].(map[string]any)
	if !reflect.DeepEqual(servers["other"], want["mcpServers"].(map[string]any)["other"]) {
		t.Errorf("other changed: %v", servers["other"])
	}
	if servers["projectlens"] == nil {
		t.Error("projectlens missing")
	}
	empty, err := RemoveMCPJSON(p)
	if err != nil || empty {
		t.Fatalf("remove: empty=%v err=%v", empty, err)
	}
	if got := decodeDoc(t, readTestFile(t, p)); !reflect.DeepEqual(got, want) {
		t.Errorf("after remove: %v, want %v", got, want)
	}
}

func TestMCPJSON_DifferentEntryHashesDiffer(t *testing.T) {
	const url = "http://u/mcp"
	for name, doc := range map[string]string{
		"extra key":      `{"mcpServers":{"projectlens":{"type":"http","url":"http://u/mcp","headers":{"a":"b"}}}}`,
		"different url":  `{"mcpServers":{"projectlens":{"type":"http","url":"http://other/mcp"}}}`,
		"different type": `{"mcpServers":{"projectlens":{"type":"sse","url":"http://u/mcp"}}}`,
	} {
		p := writeTestFile(t, doc)
		h, present, err := ReadMCPJSON(p)
		if err != nil || !present {
			t.Fatalf("%s: %v %v", name, present, err)
		}
		if h == MCPHash(url) {
			t.Errorf("%s: hash equals canonical", name)
		}
	}
}

func TestMCPJSON_StreamableHTTPAliasIsCurrent(t *testing.T) {
	p := writeTestFile(t, `{"mcpServers":{"projectlens":{"type":"streamable-http","url":"http://u/mcp"}}}`)
	h, present, err := ReadMCPJSON(p)
	if err != nil || !present || h != MCPHash("http://u/mcp") {
		t.Fatalf("%q %v %v", h, present, err)
	}
}

func TestJSON_InvalidIsRefusedUntouched(t *testing.T) {
	p := writeTestFile(t, "{not json")
	err := WriteMCPJSON(p, "http://u/mcp")
	if !errors.Is(err, ErrInvalidJSON) || !strings.Contains(err.Error(), p) {
		t.Fatalf("WriteMCPJSON err = %v", err)
	}
	if got := readTestFile(t, p); got != "{not json" {
		t.Errorf("file modified: %q", got)
	}
	err = WriteHook(p, canonicalHooks(t)[0])
	if !errors.Is(err, ErrInvalidJSON) || !strings.Contains(err.Error(), p) {
		t.Fatalf("WriteHook err = %v", err)
	}
	if got := readTestFile(t, p); got != "{not json" {
		t.Errorf("file modified: %q", got)
	}
}

func TestJSON_EmptyFileTreatedAsObject(t *testing.T) {
	for _, content := range []string{"", "  \n"} {
		p := writeTestFile(t, content)
		if err := WriteMCPJSON(p, "http://u/mcp"); err != nil {
			t.Fatalf("%q: %v", content, err)
		}
	}
}

func writeAllHooks(t *testing.T, p string) []Hook {
	t.Helper()
	hooks := canonicalHooks(t)
	for _, h := range hooks {
		if err := WriteHook(p, h); err != nil {
			t.Fatal(err)
		}
	}
	return hooks
}

func TestHooks_WriteAllThenRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	hooks := writeAllHooks(t, p)
	got, err := ReadHooks(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4, got %d", len(got))
	}
	for _, h := range hooks {
		ih, ok := got[h.ID]
		if !ok || ih.Hash() != h.Hash() || ih.Dup {
			t.Errorf("%s: %+v ok=%v", h.ID, ih, ok)
		}
	}
	doc := decodeDoc(t, readTestFile(t, p))
	events := doc["hooks"].(map[string]any)
	stop := events["Stop"].([]any)
	if len(stop) != 1 || len(stop[0].(map[string]any)["hooks"].([]any)) != 2 {
		t.Errorf("Stop hooks should share one group: %v", stop)
	}
	pre := events["PreToolUse"].([]any)
	if pre[0].(map[string]any)["matcher"] != "Edit|Write|MultiEdit" {
		t.Errorf("PreToolUse matcher: %v", pre)
	}
}

func TestHooks_PreservesForeignHooksAndKeys(t *testing.T) {
	start := `{"permissions":{"allow":["Bash(ls)"]},"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"echo foreign"}]}]}}`
	p := writeTestFile(t, start)
	hooks := writeAllHooks(t, p)
	for _, h := range hooks {
		if _, err := RemoveHook(p, h.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := decodeDoc(t, readTestFile(t, p)), decodeDoc(t, start); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHooks_ReplaceInPlaceNeverDuplicates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	h := canonicalHooks(t)[0]
	if err := WriteHook(p, h); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(readTestFile(t, p), "echo", "echo EDITED", 1)
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteHook(p, h); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readTestFile(t, p), "projectlens-hook="+h.ID); n != 1 {
		t.Errorf("want exactly one entry, got %d", n)
	}
	got, _ := ReadHooks(p)
	if got[h.ID].Hash() != h.Hash() {
		t.Error("hook not restored to canonical")
	}
}

// moveHook rewrites the file so the pre-edit hook sits under Stop in a group
// with a different matcher.
func moveHook(t *testing.T, p string, h Hook, typ string) {
	t.Helper()
	doc := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{
		"matcher": "Other",
		"hooks":   []any{map[string]any{"type": typ, "command": h.Command}},
	}}}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHooks_ReadReportsActualPlacement(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	h := canonicalHooks(t)[1]
	moveHook(t, p, h, "command")
	got, err := ReadHooks(p)
	if err != nil {
		t.Fatal(err)
	}
	ih := got[h.ID]
	if ih.Event != "Stop" || ih.Matcher != "Other" || ih.Hash() == h.Hash() {
		t.Errorf("placement not reported: %+v", ih)
	}
	moveHook(t, p, h, "prompt")
	got, _ = ReadHooks(p)
	if got[h.ID].Type != "prompt" || got[h.ID].Hash() == h.Hash() {
		t.Errorf("type change not reported: %+v", got[h.ID])
	}
}

func TestHooks_WriteRestoresPlacement(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	h := canonicalHooks(t)[1]
	moveHook(t, p, h, "command")
	if err := WriteHook(p, h); err != nil {
		t.Fatal(err)
	}
	events := decodeDoc(t, readTestFile(t, p))["hooks"].(map[string]any)
	if _, ok := events["Stop"]; ok {
		t.Errorf("Stop should be cleaned up: %v", events["Stop"])
	}
	pre := events["PreToolUse"].([]any)
	if len(pre) != 1 || pre[0].(map[string]any)["matcher"] != "Edit|Write|MultiEdit" {
		t.Errorf("PreToolUse: %v", pre)
	}
	if n := strings.Count(readTestFile(t, p), "projectlens-hook="+h.ID); n != 1 {
		t.Errorf("want one entry, got %d", n)
	}
	got, _ := ReadHooks(p)
	if got[h.ID].Hash() != h.Hash() {
		t.Error("not canonical after write")
	}
}

func TestHooks_DuplicateIDFlagged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	h := canonicalHooks(t)[1]
	entry := map[string]any{"type": "command", "command": h.Command}
	doc := map[string]any{"hooks": map[string]any{
		"PreToolUse": []any{
			map[string]any{"matcher": h.Matcher, "hooks": []any{entry}},
			map[string]any{"matcher": "Other", "hooks": []any{entry}},
		},
	}}
	b, _ := json.Marshal(doc)
	os.WriteFile(p, b, 0o644)
	got, err := ReadHooks(p)
	if err != nil || !got[h.ID].Dup {
		t.Fatalf("Dup not flagged: %+v %v", got[h.ID], err)
	}
	if err := WriteHook(p, h); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readTestFile(t, p), "projectlens-hook="+h.ID); n != 1 {
		t.Errorf("want one entry, got %d", n)
	}
	got, _ = ReadHooks(p)
	if got[h.ID].Dup {
		t.Error("Dup still set")
	}
}

func TestHooks_RemoveCleansEmptyContainers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	hooks := writeAllHooks(t, p)
	var empty bool
	var err error
	for _, h := range hooks {
		if empty, err = RemoveHook(p, h.ID); err != nil {
			t.Fatal(err)
		}
	}
	if !empty {
		t.Errorf("want empty, file: %s", readTestFile(t, p))
	}
	if got := decodeDoc(t, readTestFile(t, p)); len(got) != 0 {
		t.Errorf("want {}, got %v", got)
	}
}

func TestHooks_PreexistingEmptyEventPreserved(t *testing.T) {
	start := `{"hooks":{"Stop":[],"Notification":[{"matcher":"x","hooks":[]}]}}`
	p := writeTestFile(t, start)
	h := canonicalHooks(t)[1]
	if err := WriteHook(p, h); err != nil {
		t.Fatal(err)
	}
	events := decodeDoc(t, readTestFile(t, p))["hooks"].(map[string]any)
	if v, ok := events["Stop"]; !ok || v == nil || len(v.([]any)) != 0 {
		t.Errorf("Stop must stay []: %v", events["Stop"])
	}
	if len(events["Notification"].([]any)) != 1 {
		t.Errorf("empty group changed: %v", events["Notification"])
	}
	if _, err := RemoveHook(p, h.ID); err != nil {
		t.Fatal(err)
	}
	if got, want := decodeDoc(t, readTestFile(t, p)), decodeDoc(t, start); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestJSON_NoHTMLEscaping(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	h := Hook{ID: "x", Event: "Stop", Type: "command", Command: ": projectlens-hook=x; a && b < c > d"}
	if err := WriteHook(p, h); err != nil {
		t.Fatal(err)
	}
	if raw := readTestFile(t, p); !strings.Contains(raw, "a && b < c > d") || strings.Contains(raw, `\u00`) {
		t.Errorf("escaped: %s", raw)
	}
	q := filepath.Join(t.TempDir(), ".mcp.json")
	if err := WriteMCPJSON(q, "http://u/mcp?a=1&b=2"); err != nil {
		t.Fatal(err)
	}
	if raw := readTestFile(t, q); !strings.Contains(raw, "a=1&b=2") {
		t.Errorf("escaped: %s", raw)
	}
}

func TestEdit_PreservesFileModeAndCreates0644(t *testing.T) {
	p := writeTestFile(t, "{}")
	os.Chmod(p, 0o600)
	if err := WriteMCPJSON(p, "http://u/mcp"); err != nil {
		t.Fatal(err)
	}
	c := writeTestFile(t, "a = 1\n")
	os.Chmod(c, 0o600)
	if err := WriteCodexBlock(c, "http://u/mcp"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p, c} {
		if info, _ := os.Stat(f); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", f, info.Mode().Perm())
		}
	}
	n := filepath.Join(t.TempDir(), "new.json")
	if err := WriteMCPJSON(n, "http://u/mcp"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(n); info.Mode().Perm() != 0o644 {
		t.Errorf("new file mode = %v", info.Mode().Perm())
	}
}

func TestHooks_NullHooksErrorText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.local.json")
	write(t, p, `{"hooks": null}`)
	_, err := ReadHooks(p)
	if !errors.Is(err, ErrInvalidJSON) || strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("got %v", err)
	}
}
