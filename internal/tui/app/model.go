package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
	ctx  context.Context
	keys keyMap
	w, h int
}

func New(ctx context.Context) Model {
	return Model{ctx: ctx, keys: defaultKeys()}
}

func (m Model) Init() tea.Cmd { return nil }
