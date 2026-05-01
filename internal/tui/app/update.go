package app

import (
	"context"
	"log"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/components/confirmmodal"
	"github.com/hman-pro/projectlens/internal/tui/components/jobdrawer"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

const focusRefreshThreshold = 2 * time.Second

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Phase 2 message types (active only when WithJobs has wired the
	// runner). Handled before section routing so they can't be eaten.
	switch m2 := msg.(type) {
	case jobs.JobStartedMsg, jobs.JobLineMsg, jobs.JobTickMsg, jobs.JobBusyMsg:
		return m.handleJobMsg(m2)
	case jobs.JobCompletedMsg:
		return m.handleJobCompleted(m2)
	case jobs.PreflightDoneMsg:
		return m.handlePreflightDone(m2)
	case confirmmodal.ConfirmedMsg:
		return m.handleConfirmed(m2)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.tooSmall = m.w < minW || m.h < minH
		if !m.tooSmall {
			m.sidebar.SetSize(m.sidebarWidth(), m.h-4)
			dw, dh := m.detailSize()
			id := m.sections[m.focused].ID()
			next, cmd := m.sections[m.focused].Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
			m.sections[m.focused] = next
			return m, cmd
		}
		return m, nil

	case tickMsg:
		cmd := m.sections[m.focused].Refresh()
		return m, tea.Batch(cmd, tickCmd())

	case tea.KeyMsg:
		if m.tooSmall {
			if key.Matches(msg, m.keys.Quit) {
				return m, tea.Quit
			}
			return m, nil
		}
		// Confirm modal consumes keys when active.
		if m.confirm != nil {
			next, cmd := m.confirm.Update(msg)
			m.confirm = &next
			if next.Done() {
				m.confirm = nil
			}
			return m, cmd
		}
		// Phase 2 global action / control keys.
		if m.runner != nil {
			if next, cmd, handled := m.handleActionKey(msg); handled {
				return next, cmd
			}
			switch msg.String() {
			case "c":
				if m.runner.State().Status == "running" {
					m.runner.Cancel()
					return m, nil
				}
			case "j":
				if m.drawer != nil {
					m.drawer.Toggle()
					return m, nil
				}
			case "q":
				return m.handleQuit()
			}
		}
		if m.mode == ModeSidebar {
			return m.handleSidebarKey(msg)
		}
		return m.handleDetailKey(msg)
	}

	// Route every other message through every section so typed RefreshedMsg
	// reaches its target. Sections ignore messages that aren't their own.
	var cmds []tea.Cmd
	for i, s := range m.sections {
		next, cmd := s.Update(msg)
		m.sections[i] = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleSidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down),
		key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.ShiftTab):
		var cmd tea.Cmd
		switch {
		case key.Matches(msg, m.keys.Tab):
			m.sidebar.CursorDown()
		case key.Matches(msg, m.keys.ShiftTab):
			m.sidebar.CursorUp()
		default:
			m.sidebar, cmd = m.sidebar.Update(msg)
		}
		newIdx := m.sidebar.Index()
		var refresh tea.Cmd
		if newIdx != m.focused {
			m.focused = newIdx
			sec := m.sections[m.focused]
			if sec.LastRefresh().IsZero() || m.since() > focusRefreshThreshold {
				refresh = sec.Refresh()
			}
			dw, dh := m.detailSize()
			id := sec.ID()
			next, sizeCmd := sec.Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
			m.sections[m.focused] = next
			return m, tea.Batch(cmd, sizeCmd, refresh)
		}
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	case key.Matches(msg, m.keys.Enter):
		m.mode = ModeDetail
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: true})
		m.sections[m.focused] = next
		return m, cmd
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Esc):
		m.mode = ModeSidebar
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: false})
		m.sections[m.focused] = next
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	}
	next, cmd := m.sections[m.focused].Update(msg)
	m.sections[m.focused] = next
	return m, cmd
}

// handleActionKey dispatches a preflight when the keypress matches a
// Spec.Key. Returns handled=true once a registry key matches (whether
// or not preflight starts — binary-missing path is also "handled").
func (m Model) handleActionKey(key tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.runner.State().Status == "running" {
		return m, nil, false
	}
	s := key.String()
	if len(s) != 1 {
		return m, nil, false
	}
	for _, spec := range m.registry {
		if rune(s[0]) != spec.Key {
			continue
		}
		// Binary-missing check happens BEFORE preflight.
		if m.target.BinaryPath == "" {
			m.toastMsg = "projectlens binary not found; set PROJECTLENS_BINARY"
			return m, nil, true
		}
		m.pendingToken++
		m.pendingSpec = spec
		return m, runPreflight(m.ctx, m.store, spec, m.pendingToken), true
	}
	return m, nil, false
}

func runPreflight(ctx context.Context, s store.Store, spec jobs.Spec, token uint64) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		n, cost, err := spec.Preflight(c, s)
		return jobs.PreflightDoneMsg{Spec: spec, Count: n, Cost: cost, Err: err, Token: token}
	}
}

func (m Model) handlePreflightDone(msg jobs.PreflightDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Token != m.pendingToken {
		return m, nil
	}
	if msg.Err != nil {
		log.Printf("toast: preflight failed: %v", msg.Err)
		m.toastMsg = "preflight failed: " + msg.Err.Error()
		return m, nil
	}
	headline := msg.Spec.Headline(msg.Count, msg.Cost)
	var modal confirmmodal.Model
	if msg.Spec.Confirm == jobs.ConfirmTyped {
		modal = confirmmodal.NewTyped(headline, msg.Spec.Phrase, msg.Spec.Name)
	} else {
		modal = confirmmodal.NewYesNo(headline, msg.Spec.Name)
	}
	m.confirm = &modal
	return m, nil
}

func (m Model) handleConfirmed(msg confirmmodal.ConfirmedMsg) (tea.Model, tea.Cmd) {
	for _, spec := range m.registry {
		if spec.Name == msg.Token {
			if err := m.runner.Start(spec); err != nil {
				log.Printf("toast: start failed: %v", err)
				m.toastMsg = "start failed: " + err.Error()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m Model) handleJobMsg(_ tea.Msg) (tea.Model, tea.Cmd) {
	if m.runner == nil {
		return m, nil
	}
	snap := m.runner.State()
	if m.drawer != nil {
		m.drawer.SetState(jobdrawer.State{
			Status:  snap.Status,
			Spec:    snap.Current.Name,
			Started: snap.StartedAt,
			Tail:    snap.Tail,
			LogPath: snap.LogPath,
		}, m.w, 8)
	}
	return m, nil
}

func (m Model) handleJobCompleted(msg jobs.JobCompletedMsg) (tea.Model, tea.Cmd) {
	if m.runner == nil {
		return m, nil
	}
	snap := m.runner.State()
	if m.drawer != nil {
		m.drawer.SetState(jobdrawer.State{
			Status:   snap.Status,
			Spec:     msg.Spec.Name,
			Duration: msg.Duration,
			Tail:     msg.Tail,
			LogPath:  msg.LogPath,
		}, m.w, 8)
	}
	var cmds []tea.Cmd
	if msg.Status == "succeeded" {
		cmds = append(cmds, m.refreshSections(msg.Spec.RefreshOn))
	}
	if m.quitRequested {
		cmds = append(cmds, tea.Quit)
	}
	return m, tea.Batch(cmds...)
}

// refreshSections iterates the sections slice and dispatches Refresh()
// for each ID match. The current Model has no map, so this is a linear
// scan — fine for the small section count.
func (m Model) refreshSections(ids []string) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(ids))
	for _, id := range ids {
		for _, sec := range m.sections {
			if sec.ID() == id {
				cmds = append(cmds, sec.Refresh())
				break
			}
		}
	}
	return tea.Batch(cmds...)
}

// handleQuit drains a running job before quitting. No detach in
// Phase 2 — Ctrl+C is the OS escape hatch.
func (m Model) handleQuit() (tea.Model, tea.Cmd) {
	if m.runner == nil {
		return m, tea.Quit
	}
	st := m.runner.State().Status
	if st == "idle" || st == "succeeded" || st == "failed" || st == "cancelled" || st == "" {
		return m, tea.Quit
	}
	m.quitRequested = true
	m.runner.Cancel()
	return m, nil
}
