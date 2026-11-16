package agentsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

// ErrInvalidJSON is wrapped (with the file path) when a destination JSON file
// cannot be parsed or does not have the expected shape. The file is not modified.
var ErrInvalidJSON = errors.New("invalid JSON")

const mcpServerName = "projectlens"

type jsonObject = map[string]json.RawMessage

// marshalNoEscape is json.Marshal without HTML escaping, so values embedded as
// RawMessage keep characters such as &, < and > literal.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func invalidJSON(path string, detail error) error {
	return fmt.Errorf("%s: %w: %v", path, ErrInvalidJSON, detail)
}

// loadJSONObject reads a top-level JSON object. A missing, zero-byte, or
// whitespace-only file is an empty object; exists reports whether the file exists.
func loadJSONObject(path string) (doc jsonObject, exists bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return jsonObject{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return jsonObject{}, true, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, true, invalidJSON(path, err)
	}
	if doc == nil { // literal null
		return nil, true, invalidJSON(path, errors.New("top level is not an object"))
	}
	return doc, true, nil
}

// saveJSONObject writes doc as 2-space indented JSON with sorted keys and a
// trailing newline, without HTML escaping.
func saveJSONObject(path string, doc any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return writeFileAtomic(path, buf.Bytes())
}

// docIsEmpty reports whether the document is {} or holds only empty
// mcpServers/hooks objects.
func docIsEmpty(doc jsonObject) bool {
	for k, v := range doc {
		if k != "mcpServers" && k != "hooks" {
			return false
		}
		var m jsonObject
		if json.Unmarshal(v, &m) != nil || len(m) != 0 {
			return false
		}
	}
	return true
}

func loadMCPServers(path string, doc jsonObject) (jsonObject, error) {
	raw, ok := doc["mcpServers"]
	if !ok {
		return jsonObject{}, nil
	}
	var servers jsonObject
	if err := json.Unmarshal(raw, &servers); err != nil || servers == nil {
		return nil, invalidJSON(path, errors.New("mcpServers is not an object"))
	}
	return servers, nil
}

// ReadMCPJSON returns the hash of the mcpServers.projectlens entry.
func ReadMCPJSON(path string) (hash string, present bool, err error) {
	doc, _, err := loadJSONObject(path)
	if err != nil {
		return "", false, err
	}
	servers, err := loadMCPServers(path, doc)
	if err != nil {
		return "", false, err
	}
	entry, ok := servers[mcpServerName]
	if !ok {
		return "", false, nil
	}
	var fields map[string]any
	if json.Unmarshal(entry, &fields) == nil && len(fields) == 2 {
		typ, _ := fields["type"].(string)
		url, isStr := fields["url"].(string)
		if isStr && (typ == "http" || typ == "streamable-http") {
			return MCPHash(url), true, nil
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, entry); err != nil {
		return "", false, invalidJSON(path, err)
	}
	return HashString(compact.String()), true, nil
}

// WriteMCPJSON sets mcpServers.projectlens, creating the file if needed.
func WriteMCPJSON(path, url string) error {
	doc, _, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	servers, err := loadMCPServers(path, doc)
	if err != nil {
		return err
	}
	entry, err := marshalNoEscape(map[string]string{"type": "http", "url": url})
	if err != nil {
		return fmt.Errorf("encode MCP entry: %w", err)
	}
	servers[mcpServerName] = entry
	if doc["mcpServers"], err = marshalNoEscape(servers); err != nil {
		return fmt.Errorf("encode mcpServers: %w", err)
	}
	return saveJSONObject(path, doc)
}

// RemoveMCPJSON deletes mcpServers.projectlens. empty reports whether the
// remaining document is {} or holds only an empty mcpServers/hooks object. The
// file is left in place.
func RemoveMCPJSON(path string) (empty bool, err error) {
	doc, exists, err := loadJSONObject(path)
	if err != nil || !exists {
		return false, err
	}
	servers, err := loadMCPServers(path, doc)
	if err != nil {
		return false, err
	}
	if _, ok := servers[mcpServerName]; ok {
		delete(servers, mcpServerName)
		if len(servers) == 0 {
			delete(doc, "mcpServers")
		} else if doc["mcpServers"], err = marshalNoEscape(servers); err != nil {
			return false, fmt.Errorf("encode mcpServers: %w", err)
		}
		if err := saveJSONObject(path, doc); err != nil {
			return false, err
		}
	}
	return docIsEmpty(doc), nil
}

// InstalledHook is a hook entry found on disk, with its actual placement.
type InstalledHook struct {
	Hook
	Dup bool // the ID was found more than once
}

type hookGroup = map[string]json.RawMessage
type hookEvents = map[string][]hookGroup

type hookEntryView struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// loadHooksDoc returns the document and its decoded hooks events.
func loadHooksDoc(path string) (doc jsonObject, events hookEvents, exists bool, err error) {
	doc, exists, err = loadJSONObject(path)
	if err != nil {
		return nil, nil, exists, err
	}
	events = hookEvents{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &events); err != nil {
			return nil, nil, exists, invalidJSON(path, fmt.Errorf("hooks has an unexpected shape: %v", err))
		}
		if events == nil {
			return nil, nil, exists, invalidJSON(path, errors.New("hooks is null, not an object"))
		}
	}
	return doc, events, exists, nil
}

func groupMatcher(g hookGroup) string {
	var m string
	if raw, ok := g["matcher"]; ok {
		json.Unmarshal(raw, &m)
	}
	return m
}

func groupEntries(path string, g hookGroup) ([]json.RawMessage, error) {
	raw, ok := g["hooks"]
	if !ok {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, invalidJSON(path, fmt.Errorf("hook group has an unexpected shape: %v", err))
	}
	return entries, nil
}

func entryID(raw json.RawMessage) (string, hookEntryView) {
	var v hookEntryView
	if json.Unmarshal(raw, &v) != nil {
		return "", v
	}
	id, _ := HookID(v.Command)
	return id, v
}

// ReadHooks returns every entry carrying a ProjectLens hook ID, as found on disk.
// When an ID appears more than once, the first entry in sorted event order is
// reported, so its hash is stable from one read to the next.
func ReadHooks(path string) (map[string]InstalledHook, error) {
	_, events, _, err := loadHooksDoc(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(events))
	for event := range events {
		names = append(names, event)
	}
	sort.Strings(names)
	out := map[string]InstalledHook{}
	for _, event := range names {
		groups := events[event]
		for _, g := range groups {
			entries, err := groupEntries(path, g)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				id, v := entryID(e)
				if id == "" {
					continue
				}
				ih, seen := out[id]
				if seen {
					ih.Dup = true
				} else {
					ih = InstalledHook{Hook: Hook{ID: id, Event: event, Matcher: groupMatcher(g), Type: v.Type, Command: v.Command}}
				}
				out[id] = ih
			}
		}
	}
	return out, nil
}

// removeHookID deletes every entry carrying id, then the groups and events it
// emptied. It reports how many entries were removed.
func removeHookID(path string, events hookEvents, id string) (int, error) {
	removed := 0
	for event, groups := range events {
		var keptGroups []hookGroup
		eventRemoved := 0
		for _, g := range groups {
			entries, err := groupEntries(path, g)
			if err != nil {
				return 0, err
			}
			kept := make([]json.RawMessage, 0, len(entries))
			for _, e := range entries {
				if eid, _ := entryID(e); eid == id {
					removed++
					eventRemoved++
					continue
				}
				kept = append(kept, e)
			}
			if len(kept) != len(entries) {
				if len(kept) == 0 {
					continue // drop the group we emptied
				}
				raw, err := marshalNoEscape(kept)
				if err != nil {
					return 0, fmt.Errorf("encode hooks: %w", err)
				}
				g["hooks"] = raw
			}
			keptGroups = append(keptGroups, g)
		}
		if eventRemoved == 0 {
			continue // leave events we did not touch exactly as found
		}
		if len(keptGroups) == 0 {
			delete(events, event)
		} else {
			events[event] = keptGroups
		}
	}
	return removed, nil
}

func saveHooksDoc(path string, doc jsonObject, events hookEvents) error {
	if len(events) == 0 {
		delete(doc, "hooks")
	} else {
		raw, err := marshalNoEscape(events)
		if err != nil {
			return fmt.Errorf("encode hooks: %w", err)
		}
		doc["hooks"] = raw
	}
	return saveJSONObject(path, doc)
}

// WriteHook installs h: it removes every entry carrying h.ID, then appends the
// canonical entry to the first group under h.Event with h.Matcher.
func WriteHook(path string, h Hook) error {
	doc, events, _, err := loadHooksDoc(path)
	if err != nil {
		return err
	}
	if _, err := removeHookID(path, events, h.ID); err != nil {
		return err
	}
	entry, err := marshalNoEscape(hookEntryView{Type: h.Type, Command: h.Command})
	if err != nil {
		return fmt.Errorf("encode hook: %w", err)
	}
	groups := events[h.Event]
	placed := false
	for _, g := range groups {
		if groupMatcher(g) != h.Matcher {
			continue
		}
		entries, err := groupEntries(path, g)
		if err != nil {
			return err
		}
		raw, err := marshalNoEscape(append(entries, entry))
		if err != nil {
			return fmt.Errorf("encode hooks: %w", err)
		}
		g["hooks"] = raw
		placed = true
		break
	}
	if !placed {
		matcher, _ := marshalNoEscape(h.Matcher)
		list, _ := marshalNoEscape([]json.RawMessage{entry})
		events[h.Event] = append(groups, hookGroup{"matcher": matcher, "hooks": list})
	}
	return saveHooksDoc(path, doc, events)
}

// RemoveHook deletes every entry carrying id and cleans up the containers it
// emptied. empty reports whether the remaining document is {} or holds only an
// empty mcpServers/hooks object. The file is left in place.
func RemoveHook(path, id string) (empty bool, err error) {
	doc, events, exists, err := loadHooksDoc(path)
	if err != nil || !exists {
		return false, err
	}
	removed, err := removeHookID(path, events, id)
	if err != nil {
		return false, err
	}
	if removed > 0 {
		if err := saveHooksDoc(path, doc, events); err != nil {
			return false, err
		}
	}
	return docIsEmpty(doc), nil
}
