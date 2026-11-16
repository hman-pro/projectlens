package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrInvalidTOML is wrapped (with the file path) when a Codex config cannot be
// parsed, or when an edit would make it unparseable. The file is not modified.
var ErrInvalidTOML = errors.New("invalid TOML")

// ErrForeignCodex is returned by WriteCodexBlock when mcp_servers.projectlens is
// already defined outside the managed block.
var ErrForeignCodex = errors.New("mcp_servers.projectlens is defined outside the managed block")

const (
	codexBlockStart = "# >>> projectlens (managed by `lens agent`) >>>"
	codexBlockEnd   = "# <<< projectlens <<<"
	codexBodyHead   = "[mcp_servers.projectlens]\nurl = \""
)

// CodexState describes the ProjectLens entry in a Codex config.
type CodexState struct {
	BlockPresent bool   // the marked block exists
	BlockHash    string // MCPHash(url) when the block body is exactly canonical for some url
	Foreign      bool   // mcp_servers.projectlens is defined outside the marked block
	ForeignURL   string // its url when it is a plain string, else ""
}

func invalidTOML(path string, detail error) error {
	return fmt.Errorf("%s: %w: %v", path, ErrInvalidTOML, detail)
}

func codexBody(url string) string { return codexBodyHead + url + "\"\n" }

func codexBlock(url string) string {
	return codexBlockStart + "\n" + codexBody(url) + codexBlockEnd + "\n"
}

// splitCodexBlock cuts the marked block, found by its marker lines, out of text.
// The block spans from the start-marker line through the end-marker line,
// including that line's newline.
func splitCodexBlock(text string) (before, body, after string, found bool, err error) {
	start, bodyStart := -1, 0
	pos := 0
	for pos < len(text) {
		nl := strings.IndexByte(text[pos:], '\n')
		lineEnd, next := len(text), len(text)
		if nl >= 0 {
			lineEnd, next = pos+nl, pos+nl+1
		}
		line := strings.TrimSuffix(text[pos:lineEnd], "\r")
		switch {
		case start < 0 && line == codexBlockStart:
			start, bodyStart = pos, next
		case start >= 0 && line == codexBlockEnd:
			return text[:start], text[bodyStart:pos], text[next:], true, nil
		}
		pos = next
	}
	if start >= 0 {
		return "", "", "", false, errors.New("start marker without end marker")
	}
	return text, "", "", false, nil
}

// canonicalCodexURL returns the url when body is exactly codexBody(url).
func canonicalCodexURL(body string) (string, bool) {
	rest, ok := strings.CutPrefix(body, codexBodyHead)
	if !ok {
		return "", false
	}
	url, ok := strings.CutSuffix(rest, "\"\n")
	if !ok || strings.ContainsAny(url, "\"\\\n") {
		return "", false
	}
	return url, true
}

func parseTOML(path, text string) (map[string]any, error) {
	var m map[string]any
	if err := toml.Unmarshal([]byte(text), &m); err != nil {
		return nil, invalidTOML(path, err)
	}
	return m, nil
}

func readCodexText(path string) (text string, exists bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), true, nil
}

// ReadCodexConfig reports the state of the ProjectLens entry in a Codex config.
func ReadCodexConfig(path string) (CodexState, error) {
	text, _, err := readCodexText(path)
	if err != nil {
		return CodexState{}, err
	}
	return codexStateFromText(path, text)
}

func codexStateFromText(path, text string) (CodexState, error) {
	before, body, after, found, err := splitCodexBlock(text)
	if err != nil {
		return CodexState{}, invalidTOML(path, err)
	}
	var st CodexState
	if found {
		st.BlockPresent = true
		if url, ok := canonicalCodexURL(body); ok {
			st.BlockHash = MCPHash(url)
		} else {
			st.BlockHash = HashString(body)
		}
	}
	m, err := parseTOML(path, before+after)
	if err != nil {
		return CodexState{}, err
	}
	if servers, ok := m["mcp_servers"].(map[string]any); ok {
		if entry, ok := servers[mcpServerName]; ok {
			st.Foreign = true
			if t, ok := entry.(map[string]any); ok {
				st.ForeignURL, _ = t["url"].(string)
			}
		}
	}
	return st, nil
}

// WriteCodexBlock installs the marked block, replacing an existing one in place.
func WriteCodexBlock(path, url string) error {
	text, _, err := readCodexText(path)
	if err != nil {
		return err
	}
	st, err := codexStateFromText(path, text)
	if err != nil {
		return err
	}
	if st.Foreign {
		return fmt.Errorf("%s: %w", path, ErrForeignCodex)
	}
	var out string
	if before, _, after, found, _ := splitCodexBlock(text); found {
		out = before + codexBlock(url) + after
	} else {
		out = text
		if out != "" {
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += "\n"
		}
		out += codexBlock(url)
	}
	m, err := parseTOML(path, out)
	if err != nil {
		return err
	}
	// The url must round-trip exactly, or it altered the document (injection).
	servers, _ := m["mcp_servers"].(map[string]any)
	entry, _ := servers[mcpServerName].(map[string]any)
	if got, _ := entry["url"].(string); got != url {
		return invalidTOML(path, errors.New("url does not round-trip through the managed block"))
	}
	return writeFileAtomic(path, []byte(out))
}

// RemoveCodexBlock removes the marked block and the one blank line written
// before it. empty reports whether only whitespace remains. The file is left in
// place.
func RemoveCodexBlock(path string) (empty bool, err error) {
	text, exists, err := readCodexText(path)
	if err != nil || !exists {
		return false, err
	}
	before, _, after, found, err := splitCodexBlock(text)
	if err != nil {
		return false, invalidTOML(path, err)
	}
	if !found {
		if _, err := parseTOML(path, text); err != nil {
			return false, err
		}
		return strings.TrimSpace(text) == "", nil
	}
	if strings.HasSuffix(before, "\n\n") {
		before = before[:len(before)-1]
	}
	out := before + after
	if _, err := parseTOML(path, out); err != nil {
		return false, err
	}
	if err := writeFileAtomic(path, []byte(out)); err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}
