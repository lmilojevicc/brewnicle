package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/platform"
)

const startupRenderDelay = 50 * time.Millisecond

func waitStartupFrame() tea.Cmd {
	return tea.Tick(startupRenderDelay, func(time.Time) tea.Msg { return startupFrameMsg{} })
}

func waitRefresh(ch <-chan RefreshEvent) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return refreshEventMsg{Event: RefreshEvent{Done: true, Err: errors.New("refresh ended without a result")}}
		}
		return refreshEventMsg{Event: e}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = x.Width, x.Height
		m.input.Width = m.searchInputWidth()
		if isWideLayout(m.width, m.height) && m.state == StateNarrowDetail {
			m.state = StateBrowse
		}
		if m.pendingRefresh && !m.startupFramePending && isUsableLayout(m.width, m.height) {
			m.startupFramePending = true
			return m, waitStartupFrame()
		}
		return m, nil
	case startupFrameMsg:
		m.startupFramePending = false
		if !m.pendingRefresh {
			return m, nil
		}
		m.pendingRefresh = false
		return m, func() tea.Msg { return startRefreshMsg{} }
	case startRefreshMsg:
		if m.refreshing || m.deps.Refresh == nil {
			return m, nil
		}
		m.refreshing = true
		m.pendingRefresh = false
		m.progress = "starting refresh"
		if m.dates == DatesUnavailable {
			m.dates = DatesIndexing
			m.setStatus("Retrying exact package date indexing", statusWarning)
		}
		m.refreshCh = m.deps.Refresh()
		return m, waitRefresh(m.refreshCh)
	case refreshEventMsg:
		return m.updateRefreshEvent(x.Event)
	case ActionResultMsg:
		m.clearConfirmation()
		m.state = StateBrowse
		if x.Err != nil {
			m.setStatus(x.Action+" failed: "+x.Err.Error(), statusError)
		} else {
			m.setStatus(x.Action+" complete", statusSuccess)
		}
		return m, nil
	case platform.ExecResult:
		m.clearConfirmation()
		m.state = StateBrowse
		if x.Err != nil {
			m.setStatus("Install failed: "+x.Err.Error(), statusError)
		} else {
			m.setStatus("Install complete", statusSuccess)
		}
		return m, nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	key := km.String()

	// The terminal-too-small screen hides modal content and owns the only
	// visible escape control. At usable sizes, search and confirmation own all
	// text keys, including q and ctrl+c.
	if m.width > 0 && m.height > 0 && !isUsableLayout(m.width, m.height) {
		if key == "q" || key == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.state == StateSearch {
		return m.updateSearch(km)
	}
	if m.state == StateConfirm {
		switch key {
		case "esc", "n":
			m.clearConfirmation()
			m.state = StateBrowse
			return m, nil
		case "enter", "y":
			if !m.hasConfirmation {
				m.state = StateBrowse
				m.setStatus("Install confirmation expired", statusWarning)
				return m, nil
			}
			p := m.confirmPackage
			if m.deps.Install == nil {
				m.clearConfirmation()
				m.state = StateBrowse
				m.setStatus(m.installUnavailableMessage(), statusWarning)
				return m, nil
			}
			m.clearConfirmation()
			m.state = StateRunningInstall
			return m, m.deps.Install(p)
		}
		return m, nil
	}
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.state == StateHelp {
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?", "esc":
			m.state = m.previous
		}
		return m, nil
	}
	if m.state == StateFatal {
		if key == "r" && !m.refreshing {
			return m, func() tea.Msg { return startRefreshMsg{} }
		}
		if key == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.state == StateRunningInstall {
		return m, nil
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected+1 < len(m.visible) {
			m.selected++
		}
	case "1", "2", "3", "4", "5":
		m.changeRange(domainRange(int(key[0] - '1')))
	case "tab":
		m.changeRange(cycle(m.rangeValue, 1))
	case "shift+tab":
		m.changeRange(cycle(m.rangeValue, -1))
	case "f":
		m.changeKindFilter(domain.CycleKindFilter(m.kindFilter, 1))
	case "F":
		m.changeKindFilter(domain.CycleKindFilter(m.kindFilter, -1))
	case "/":
		m.state = StateSearch
		m.input.Focus()
		return m, textinputBlink()
	case "esc":
		if m.query != "" {
			m.input.SetValue("")
			m.applyFilter("")
		}
	case "?":
		m.previous = m.state
		m.state = StateHelp
	case "r":
		if !m.refreshing {
			return m, func() tea.Msg { return startRefreshMsg{} }
		}
	case "o":
		if p, selected := m.selectedPackage(); selected && p.Homepage != "" {
			if m.deps.Open != nil {
				return m, m.deps.Open(p)
			}
		} else {
			m.setStatus("Homepage unavailable", statusWarning)
		}
	case "i":
		if p, selected := m.selectedPackage(); selected {
			if m.deps.Install == nil {
				m.setStatus(m.installUnavailableMessage(), statusWarning)
				break
			}
			m.confirmPackage = p
			m.hasConfirmation = true
			m.state = StateConfirm
		}
	case "enter":
		if isNarrowLayout(m.width, m.height) {
			if m.state == StateNarrowDetail {
				m.state = StateBrowse
			} else {
				m.state = StateNarrowDetail
			}
		}
	}
	return m, nil
}

func (m Model) updateRefreshEvent(e RefreshEvent) (tea.Model, tea.Cmd) {
	if e.Progress != nil {
		m.progress = e.Progress.Detail
	}
	if e.SnapshotReady {
		key := ""
		if p, selected := m.selectedPackage(); selected {
			key = p.Key()
		}
		canceled := m.cancelMissingConfirmation(e.Packages)
		m.packages = append([]domain.Package(nil), e.Packages...)
		m.dates = DatesReady
		m.applyFilter(key)
		m.stale = false
		if !canceled {
			m.setStatus("Loaded package index published by another Brewnicle process", statusSuccess)
		}
		if m.state == StateBootstrap || m.state == StateFatal {
			m.state = StateBrowse
		}
		return m, waitRefresh(m.refreshCh)
	}
	if e.CatalogReady {
		key := ""
		if p, selected := m.selectedPackage(); selected {
			key = p.Key()
		}
		canceled := m.cancelMissingConfirmation(e.Packages)
		m.packages = append([]domain.Package(nil), e.Packages...)
		m.dates = DatesIndexing
		m.rangeValue = domain.RangeAll
		m.applyFilter(key)
		m.stale = false
		if !canceled {
			m.setStatus("Showing the current catalog while exact package dates index", statusWarning)
		}
		if m.state == StateBootstrap || m.state == StateFatal {
			m.state = StateBrowse
		}
		return m, waitRefresh(m.refreshCh)
	}
	if !e.Done {
		return m, waitRefresh(m.refreshCh)
	}
	m.refreshing = false
	m.progress = ""
	if e.Err != nil {
		if len(m.packages) == 0 {
			m.state = StateFatal
			if m.bootstrapDiagnostic != "" {
				m.setStatus(m.bootstrapDiagnostic+"; rebuild failed: "+e.Err.Error(), statusError)
			} else {
				m.setStatus(e.Err.Error(), statusError)
			}
		} else if m.dates == DatesIndexing {
			m.dates = DatesUnavailable
			m.setStatus("Exact package dates unavailable: "+e.Err.Error()+"; press r to retry", statusError)
		} else {
			m.setStatus("Refresh failed: "+e.Err.Error(), statusError)
		}
		return m, nil
	}
	key := ""
	if p, selected := m.selectedPackage(); selected {
		key = p.Key()
	}
	confirmationCanceled := false
	if e.Packages != nil {
		confirmationCanceled = m.cancelMissingConfirmation(e.Packages)
		m.packages = e.Packages
	}
	m.dates = DatesReady
	m.applyFilter(key)
	m.stale = false
	m.bootstrapDiagnostic = ""
	parts := []string{fmt.Sprintf("Refreshed %d packages", len(m.packages))}
	level := statusSuccess
	if e.Summary.SkippedFormulae != 0 || e.Summary.SkippedCasks != 0 {
		parts = append(parts, fmt.Sprintf("skipped %d formulae, %d casks", e.Summary.SkippedFormulae, e.Summary.SkippedCasks))
		level = statusWarning
	}
	if e.Summary.Warning != "" {
		parts = append(parts, e.Summary.Warning)
		level = statusWarning
	}
	if !confirmationCanceled {
		m.setStatus(strings.Join(parts, "; "), level)
	}
	if m.state == StateBootstrap || m.state == StateFatal {
		m.state = StateBrowse
	}
	return m, nil
}

func (m *Model) changeRange(next domain.Range) {
	if m.dates != DatesReady {
		m.rangeValue = domain.RangeAll
		if next != domain.RangeAll {
			m.setStatus("Date ranges are available after exact package dates finish indexing", statusWarning)
		}
		return
	}
	keep := ""
	if p, selected := m.selectedPackage(); selected {
		keep = p.Key()
	}
	m.rangeValue = next
	m.applyFilter(keep)
}

func (m *Model) changeKindFilter(next domain.KindFilter) {
	keep := ""
	if p, selected := m.selectedPackage(); selected {
		keep = p.Key()
	}
	m.kindFilter = next
	m.applyFilter(keep)
}

func (m Model) updateSearch(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		m.input.Blur()
		m.state = StateBrowse
		m.applyFilter("")
		return m, nil
	case "esc":
		m.input.Blur()
		m.state = StateBrowse
		m.applyFilter("")
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	m.applyFilter("")
	return m, cmd
}

func (m Model) installUnavailableMessage() string {
	if m.deps.InstallUnavailable != "" {
		return m.deps.InstallUnavailable
	}
	return "Homebrew is unavailable"
}
