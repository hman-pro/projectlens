package agentsetup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

var foreignCodexSpellings = []string{
	"[mcp_servers.projectlens]\nurl = \"z\"\n",
	"[mcp_servers.\"projectlens\"]\nurl = \"z\"\n",
	"[mcp_servers . projectlens]\nurl = \"z\"\n",
	"[mcp_servers.'projectlens']\nurl = \"z\"\n",
	"mcp_servers.projectlens.url = \"z\"\n",
	"[mcp_servers]\nprojectlens = { url = \"z\" }\n",
	"mcp_servers = { projectlens = { url = \"z\" } }\n",
}

func TestCodexBlock_AppendAndRemoveRestoresBytes(t *testing.T) {
	orig := "model = \"x\"\n[other]\nk = 1\n"
	p := writeTestFile(t, orig)
	if err := WriteCodexBlock(p, "http://u/mcp"); err != nil {
		t.Fatal(err)
	}
	want := orig + "\n# >>> projectlens (managed by `lens agent`) >>>\n[mcp_servers.projectlens]\nurl = \"http://u/mcp\"\n# <<< projectlens <<<\n"
	if got := readTestFile(t, p); got != want {
		t.Fatalf("written:\n%q\nwant:\n%q", got, want)
	}
	empty, err := RemoveCodexBlock(p)
	if err != nil || empty {
		t.Fatalf("remove: %v %v", empty, err)
	}
	if got := readTestFile(t, p); got != orig {
		t.Errorf("got %q, want %q", got, orig)
	}
}

func TestCodexBlock_CreateOnMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".codex", "config.toml")
	if st, err := ReadCodexConfig(p); err != nil || st != (CodexState{}) {
		t.Fatalf("missing: %+v %v", st, err)
	}
	if err := WriteCodexBlock(p, "http://u/mcp"); err != nil {
		t.Fatal(err)
	}
	st, err := ReadCodexConfig(p)
	if err != nil || !st.BlockPresent || st.BlockHash != MCPHash("http://u/mcp") || st.Foreign {
		t.Fatalf("state: %+v %v", st, err)
	}
	empty, err := RemoveCodexBlock(p)
	if err != nil || !empty {
		t.Fatalf("remove: %v %v", empty, err)
	}
}

func TestCodexBlock_ReplaceInPlace(t *testing.T) {
	p := writeTestFile(t, "a = 1\n")
	if err := WriteCodexBlock(p, "http://one/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(readTestFile(t, p)+"b = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteCodexBlock(p, "http://two/mcp"); err != nil {
		t.Fatal(err)
	}
	got := readTestFile(t, p)
	if strings.Count(got, ">>> projectlens") != 1 || !strings.Contains(got, "http://two/mcp") || strings.Contains(got, "http://one/mcp") {
		t.Errorf("not replaced: %q", got)
	}
	if !strings.HasSuffix(got, "b = 2\n") {
		t.Errorf("block should stay in place: %q", got)
	}
}

func TestCodexBlock_EditedBodyHashDiffers(t *testing.T) {
	p := writeTestFile(t, "")
	if err := WriteCodexBlock(p, "http://u/mcp"); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(readTestFile(t, p), "url = ", "startup_timeout_sec = 5\nurl = ", 1)
	os.WriteFile(p, []byte(edited), 0o644)
	st, err := ReadCodexConfig(p)
	if err != nil || !st.BlockPresent || st.BlockHash == "" || st.BlockHash == MCPHash("http://u/mcp") {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestCodexConfig_ForeignDetectedInEverySpelling(t *testing.T) {
	for _, in := range foreignCodexSpellings {
		p := writeTestFile(t, in)
		st, err := ReadCodexConfig(p)
		if err != nil || !st.Foreign || st.ForeignURL != "z" || st.BlockPresent {
			t.Errorf("%q: %+v %v", in, st, err)
		}
	}
	for name, in := range map[string]string{
		"other server": "[mcp_servers.other]\nurl = \"z\"\n",
		"in a string":  "note = \"\"\"\n[mcp_servers.projectlens]\nurl = \"z\"\n\"\"\"\n",
	} {
		st, err := ReadCodexConfig(writeTestFile(t, in))
		if err != nil || st.Foreign {
			t.Errorf("%s: %+v %v", name, st, err)
		}
	}
}

func TestCodexBlock_WriteRefusesForeign(t *testing.T) {
	for _, in := range foreignCodexSpellings {
		p := writeTestFile(t, in)
		if err := WriteCodexBlock(p, "http://u/mcp"); !errors.Is(err, ErrForeignCodex) {
			t.Errorf("%q: err = %v", in, err)
		}
		if got := readTestFile(t, p); got != in {
			t.Errorf("%q modified to %q", in, got)
		}
	}
}

func TestCodexBlock_ResultAlwaysParses(t *testing.T) {
	const url = "http://u/mcp"
	for name, in := range map[string]string{
		"empty":                   "",
		"keys only":               "model = \"x\"\n",
		"other tables":            "[a]\nk = 1\n[b.c]\nj = 2\n",
		"comment without newline": "# trailing comment",
		"mcp_servers other":       "[mcp_servers.other]\nurl = \"q\"\n",
	} {
		p := writeTestFile(t, in)
		if err := WriteCodexBlock(p, url); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var m map[string]any
		if _, err := toml.Decode(readTestFile(t, p), &m); err != nil {
			t.Errorf("%s: does not parse: %v", name, err)
			continue
		}
		got := m["mcp_servers"].(map[string]any)["projectlens"].(map[string]any)["url"]
		if got != url {
			t.Errorf("%s: url = %v", name, got)
		}
	}
}

func TestCodexConfig_InvalidTOMLRefused(t *testing.T) {
	p := writeTestFile(t, "[broken")
	if _, err := ReadCodexConfig(p); !errors.Is(err, ErrInvalidTOML) {
		t.Errorf("read err = %v", err)
	}
	if err := WriteCodexBlock(p, "http://u/mcp"); !errors.Is(err, ErrInvalidTOML) || !strings.Contains(err.Error(), p) {
		t.Errorf("write err = %v", err)
	}
	if _, err := RemoveCodexBlock(p); !errors.Is(err, ErrInvalidTOML) {
		t.Errorf("remove err = %v", err)
	}
	if got := readTestFile(t, p); got != "[broken" {
		t.Errorf("modified: %q", got)
	}
}

func TestCodexBlock_URLInjectionRefused(t *testing.T) {
	orig := "a = 1\n"
	p := writeTestFile(t, orig)
	err := WriteCodexBlock(p, "x\"\nfoo = \"y")
	if !errors.Is(err, ErrInvalidTOML) {
		t.Fatalf("err = %v", err)
	}
	if got := readTestFile(t, p); got != orig {
		t.Errorf("modified: %q", got)
	}
}
