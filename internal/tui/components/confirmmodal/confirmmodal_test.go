package confirmmodal_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/components/confirmmodal"
)

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestYesNo_Y_Confirms(t *testing.T) {
	m := confirmmodal.NewYesNo("ok?", "TOKEN")
	next, cmd := m.Update(keyRunes("y"))
	if !next.Done() || !next.Confirmed() {
		t.Fatalf("y did not confirm: %+v", next)
	}
	if cmd == nil {
		t.Fatal("expected dispatch cmd")
	}
	if msg, ok := cmd().(confirmmodal.ConfirmedMsg); !ok || msg.Token != "TOKEN" {
		t.Fatalf("dispatch mismatch: %+v", msg)
	}
}

func TestYesNo_N_Cancels(t *testing.T) {
	for _, k := range []string{"n", "N", "x"} {
		m := confirmmodal.NewYesNo("ok?", "TOKEN")
		next, _ := m.Update(keyRunes(k))
		if !next.Done() || next.Confirmed() {
			t.Fatalf("%s should cancel: %+v", k, next)
		}
	}
}

func TestTyped_RequiresExactPhrase(t *testing.T) {
	m := confirmmodal.NewTyped("are you sure?", "reindex", "RUN")
	for _, r := range "reindex" {
		next, _ := m.Update(keyRunes(string(r)))
		m = next
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !next.Done() || !next.Confirmed() {
		t.Fatal("typed exact phrase + enter should confirm")
	}
}

func TestTyped_PartialPhraseEnterDoesNothing(t *testing.T) {
	m := confirmmodal.NewTyped("sure?", "reindex", "RUN")
	for _, r := range "rein" {
		next, _ := m.Update(keyRunes(string(r)))
		m = next
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.Done() {
		t.Fatal("partial phrase must not be Done on enter")
	}
}

func TestEsc_Cancels(t *testing.T) {
	m := confirmmodal.NewYesNo("ok?", "TOKEN")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !next.Done() || next.Confirmed() {
		t.Fatal("esc must cancel")
	}
}

func TestView_RendersHeadline(t *testing.T) {
	m := confirmmodal.NewYesNo("hello world", "T")
	if !strings.Contains(m.View(), "hello world") {
		t.Fatal("view missing headline")
	}
}
