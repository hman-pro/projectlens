package app

import (
	"context"
	"log"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/components/confirmmodal"
	"github.com/hman-pro/projectlens/internal/tui/components/errormodal"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	jobssec "github.com/hman-pro/projectlens/internal/tui/sections/jobs"
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
		// Error modal consumes keys when active. Takes precedence over
		// confirm because we want users to acknowledge a failure before
		// kicking off another action.
		if m.errorModal != nil {
			next, cmd := m.errorModal.Update(msg)
			m.errorModal = &next
			if next.Done() {
				m.errorModal = nil
			}
			return m, cmd
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
			case "J":
				next, cmd := m.focusSection(jobssec.ID)
				return next, cmd
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
			em := errormodal.New("binary not found",
				"projectlens binary could not be located.").
				WithHint("set PROJECTLENS_BINARY, place a projectlens binary next to projectlens-tui, or add it to PATH")
			m.errorModal = &em
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
		log.Printf("preflight failed: %v", msg.Err)
		em := errormodal.New("preflight failed", msg.Err.Error())
		m.errorModal = &em
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
				log.Printf("start failed: %v", err)
				em := errormodal.New(spec.Name+" start failed", err.Error())
				m.errorModal = &em
			}
			return m, nil
		}
	}
	return m, nil
}

func (m Model) handleJobMsg(raw tea.Msg) (tea.Model, tea.Cmd) {
	if m.runner == nil {
		return m, nil
	}
	snap := m.runner.State()
	live := jobssec.LiveStateMsg{
		Status:  snap.Status,
		Spec:    snap.Current.Name,
		Started: snap.StartedAt,
		Tail:    snap.Tail,
		LogPath: snap.LogPath,
	}
	cmds := []tea.Cmd{m.broadcastToJobs(live)}
	// On the first event of a fresh run (a JobStartedMsg), pull the
	// user into the Jobs section so they see the live tail without
	// having to navigate.
	if _, ok := raw.(jobs.JobStartedMsg); ok {
		next, focusCmd := m.focusSection(jobssec.ID)
		m = next
		if focusCmd != nil {
			cmds = append(cmds, focusCmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// resyncDetailSize re-issues a SizeMsg to the focused section so it
// can lay out against current dimensions. Returns the model and any
// size cmd.
func (m Model) resyncDetailSize() (Model, tea.Cmd) {
	if m.tooSmall || len(m.sections) == 0 {
		return m, nil
	}
	dw, dh := m.detailSize()
	id := m.sections[m.focused].ID()
	next, cmd := m.sections[m.focused].Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
	m.sections[m.focused] = next
	return m, cmd
}

// broadcastToJobs delivers a message to the Jobs section by ID match
// instead of the running-section route. Used to feed live-job state
// without requiring the user to focus Jobs first.
func (m Model) broadcastToJobs(msg tea.Msg) tea.Cmd {
	for i, s := range m.sections {
		if s.ID() != jobssec.ID {
			continue
		}
		next, cmd := s.Update(msg)
		m.sections[i] = next
		return cmd
	}
	return nil
}

// focusSection switches the focused section to the one with the given
// ID. Returns the updated model and any size cmd from the new
// focused section.
func (m Model) focusSection(id string) (Model, tea.Cmd) {
	for i, s := range m.sections {
		if s.ID() != id {
			continue
		}
		if i == m.focused {
			return m, nil
		}
		// Blur the current section.
		curID := m.sections[m.focused].ID()
		blurred, _ := m.sections[m.focused].Update(sections.FocusMsg{SectionID: curID, Focused: false})
		m.sections[m.focused] = blurred
		m.focused = i
		m.sidebar.Select(i)
		// Focus + size new section.
		focused, _ := s.Update(sections.FocusMsg{SectionID: id, Focused: true})
		m.sections[i] = focused
		next, cmd := m.resyncDetailSize()
		return next, cmd
	}
	return m, nil
}

func (m Model) handleJobCompleted(msg jobs.JobCompletedMsg) (tea.Model, tea.Cmd) {
	if m.runner == nil {
		return m, nil
	}
	live := jobssec.LiveStateMsg{
		Status:   msg.Status,
		Spec:     msg.Spec.Name,
		Duration: msg.Duration,
		Tail:     msg.Tail,
		LogPath:  msg.LogPath,
	}
	cmds := []tea.Cmd{m.broadcastToJobs(live)}
	if msg.Status == "succeeded" {
		cmds = append(cmds, m.refreshSections(msg.Spec.RefreshOn))
	}
	if msg.Status == "failed" && m.errorModal == nil {
		em := errormodal.New(msg.Spec.Name+" failed", lastErrLine(msg.Tail))
		if msg.LogPath != "" {
			em = em.WithHint("log: " + msg.LogPath)
		}
		m.errorModal = &em
	}
	// Jobs section reflects every terminal status, not just success.
	cmds = append(cmds, m.refreshSections([]string{"jobs"}))
	if m.quitRequested {
		cmds = append(cmds, tea.Quit)
	}
	return m, tea.Batch(cmds...)
}

// lastErrLine returns the trailing non-empty line from a job's tail —
// usually the most informative failure message. Falls back to a generic
// string when the tail is empty.
func lastErrLine(tail []string) string {
	for i := len(tail) - 1; i >= 0; i-- {
		if s := tail[i]; s != "" {
			return s
		}
	}
	return "subprocess exited with non-zero status"
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
