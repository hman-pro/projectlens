package errormodal_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/components/errormodal"
)

func TestDismissOnEsc(t *testing.T) {
	m := errormodal.New("boom", "stack trace")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !next.Done() {
		t.Fatal("esc must dismiss")
	}
}

func TestDismissOnEnter(t *testing.T) {
	m := errormodal.New("boom", "stack trace")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !next.Done() {
		t.Fatal("enter must dismiss")
	}
}

func TestDismissOnQ(t *testing.T) {
	m := errormodal.New("boom", "stack trace")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !next.Done() {
		t.Fatal("q must dismiss")
	}
}

func TestIgnoresNonKeyMsg(t *testing.T) {
	m := errormodal.New("boom", "msg")
	next, _ := m.Update(struct{}{})
	if next.Done() {
		t.Fatal("non-key msg must not dismiss")
	}
}

func TestViewContainsTitleMessageHint(t *testing.T) {
	m := errormodal.New("preflight failed", "context deadline exceeded").
		WithHint("log: /tmp/foo.log")
	v := m.View()
	for _, want := range []string{"preflight failed", "context deadline exceeded", "log: /tmp/foo.log", "esc/enter"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}

func TestViewWithoutHint(t *testing.T) {
	m := errormodal.New("boom", "msg")
	v := m.View()
	if !strings.Contains(v, "boom") || !strings.Contains(v, "msg") {
		t.Fatal("view missing core text")
	}
}
