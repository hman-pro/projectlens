package pipeline

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen != m.gen {
			return m, nil
		}
		m.last = time.Now()
		if msg.Err != nil {
			m.err = msg.Err
			m.status = sections.StatusError
			return m, nil
		}
		m.snap = msg.Snap
		m.err = nil
		m.status = sections.StatusOK
		m.refreshContent()
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		// reserve 2 rows for the section's footer hint
		vph := max(3, msg.H-2)
		m.vp.Width = msg.W
		m.vp.Height = vph
		m.refreshContent()
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		m.refreshContent()
		return m, nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		switch strings.ToLower(msg.String()) {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
				m.refreshContent()
				m.scrollSelectedIntoView()
			}
			return m, nil
		case "down", "j":
			if m.selected < len(stageOrder)-1 {
				m.selected++
				m.refreshContent()
				m.scrollSelectedIntoView()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

// refreshContent rebuilds the joined card block and writes it to the
// viewport.
func (m *Model) refreshContent() {
	cards, _ := m.renderCards()
	m.vp.SetContent(strings.Join(cards, "\n"))
}

// renderCards returns per-card strings + the line offset of each
// card's first row in the joined block.
func (m *Model) renderCards() (cards []string, offsets []int) {
	cards = make([]string, 0, len(stageOrder))
	offsets = make([]int, 0, len(stageOrder))
	stats := indexStages(m.snap)
	width := m.w
	if width <= 0 {
		width = 60
	}
	row := 0
	for i, def := range stageOrder {
		stat, has := stats[def.ID]
		card := renderCard(def, stat, has, i == m.selected, m.focused, width)
		cards = append(cards, card)
		offsets = append(offsets, row)
		row += strings.Count(card, "\n") + 1 + 1 // card lines + join separator
	}
	return cards, offsets
}

// scrollSelectedIntoView nudges the viewport so the selected card is
// visible.
func (m *Model) scrollSelectedIntoView() {
	cards, offsets := m.renderCards()
	if m.selected < 0 || m.selected >= len(cards) {
		return
	}
	top := offsets[m.selected]
	bot := top + strings.Count(cards[m.selected], "\n")
	if top < m.vp.YOffset {
		m.vp.SetYOffset(top)
		return
	}
	if bot >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(bot - m.vp.Height + 1)
	}
}

func indexStages(snap store.PipelineSnapshot) map[string]StageStat {
	out := make(map[string]StageStat, len(snap.Stages))
	for _, s := range snap.Stages {
		out[s.Name] = s
	}
	return out
}
