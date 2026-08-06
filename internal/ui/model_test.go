package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/platform"
	"github.com/milo/brewnicle/internal/refresh"
)

func uiPkgs() []domain.Package {
	now := time.Now().UTC()
	older := now.Add(-2 * time.Hour)
	return []domain.Package{
		{Name: "new", Kind: domain.KindFormula, Description: "needle", Homepage: "https://example.test/new", InstallTarget: "new", AddedAt: &now},
		{Name: "second", Kind: domain.KindFormula, Description: "other", Homepage: "https://example.test/second", InstallTarget: "second", AddedAt: &older},
		{Name: "unknown", Kind: domain.KindCask, InstallTarget: "unknown"},
	}
}

func update(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	model, _ := m.Update(msg)
	return model.(Model)
}

func updateWithCmd(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	model, cmd := m.Update(msg)
	return model.(Model), cmd
}

func runeKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func commandIsQuit(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestDefaultRangeMovementAndRangeKeys(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{})
	if m.Range() != domain.Range30D || len(m.Visible()) != 2 {
		t.Fatal(m.Range(), m.Visible())
	}
	m = update(t, m, runeKey("j"))
	if p, _ := m.selectedPackage(); p.Name != "second" {
		t.Fatal(p)
	}
	m = update(t, m, runeKey("k"))
	if p, _ := m.selectedPackage(); p.Name != "new" {
		t.Fatal(p)
	}
	for i, want := range domain.Ranges {
		m = update(t, m, runeKey(string(rune('1'+i))))
		if m.Range() != want {
			t.Fatalf("key %d got %s want %s", i+1, m.Range(), want)
		}
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.Range() != domain.Range7D {
		t.Fatal(m.Range())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.Range() != domain.RangeAll {
		t.Fatal(m.Range())
	}
}

func TestStartupRefreshWaitsForFirstUsableFrameAndDeduplicates(t *testing.T) {
	ch := make(chan RefreshEvent, 1)
	starts := 0
	m := New(uiPkgs(), false, true, true, Dependencies{Refresh: func() <-chan RefreshEvent {
		starts++
		return ch
	}})
	if m.Init() != nil || !m.PendingRefresh() {
		t.Fatal("Init must not start work")
	}
	m, cmd := updateWithCmd(t, m, tea.WindowSizeMsg{Width: 49, Height: 11})
	if cmd != nil || starts != 0 || !m.PendingRefresh() {
		t.Fatal("refresh started before a usable frame")
	}
	m, frameCmd := updateWithCmd(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	if frameCmd == nil || starts != 0 || !m.PendingRefresh() || !m.startupFramePending {
		t.Fatal("usable frame did not schedule one post-render boundary")
	}
	if !strings.Contains(m.View(), "STALE") || m.Refreshing() {
		t.Fatal("cached stale frame not visible before boundary")
	}
	m, cmd = updateWithCmd(t, m, frameCmd())
	if cmd == nil || starts != 0 || m.PendingRefresh() || m.startupFramePending {
		t.Fatal("post-render boundary did not queue exactly one start")
	}
	m, wait := updateWithCmd(t, m, cmd())
	if starts != 1 || !m.Refreshing() || wait == nil {
		t.Fatal(starts, m.Refreshing())
	}
	m, _ = updateWithCmd(t, m, startRefreshMsg{})
	if starts != 1 {
		t.Fatal("duplicate refresh")
	}
	ch <- RefreshEvent{Packages: uiPkgs(), Done: true}
	m = update(t, m, wait())
	if m.Refreshing() {
		t.Fatal("refresh still active")
	}
}

func TestSearchIsVisibleAndEscapeIsTwoStage(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = update(t, m, runeKey("/"))
	if m.State() != StateSearch || !m.input.Focused() || !strings.Contains(m.View(), "Search:") {
		t.Fatal("focused empty search is invisible")
	}
	m = update(t, m, runeKey("needle"))
	if len(m.Visible()) != 1 || !strings.Contains(m.View(), "needle") {
		t.Fatal(m.Visible())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.State() != StateBrowse || len(m.Visible()) != 1 {
		t.Fatal("first escape must keep query")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.Visible()) != 2 || m.query != "" {
		t.Fatal("second escape must clear query")
	}
	m = update(t, m, runeKey("/"))
	m = update(t, m, runeKey("other"))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.State() != StateBrowse || len(m.Visible()) != 1 || m.query != "other" {
		t.Fatal("enter did not commit search")
	}
}

func TestLayoutAndQuitOwnership(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 15})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.State() != StateNarrowDetail {
		t.Fatal("wide-but-short layout must use narrow details")
	}
	m = update(t, m, runeKey("?"))
	if m.State() != StateHelp {
		t.Fatal()
	}
	_, cmd := updateWithCmd(t, m, runeKey("q"))
	if !commandIsQuit(t, cmd) {
		t.Fatal("q did not quit help")
	}
	m.state = StateSearch
	m, cmd = updateWithCmd(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if commandIsQuit(t, cmd) || m.State() != StateSearch {
		t.Fatal("search must own ctrl+c")
	}
	m.state = StateConfirm
	m, cmd = updateWithCmd(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if commandIsQuit(t, cmd) || m.State() != StateConfirm {
		t.Fatal("confirmation must own ctrl+c")
	}
	m.state = StateHelp
	_, cmd = updateWithCmd(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !commandIsQuit(t, cmd) {
		t.Fatal("ctrl+c did not quit help")
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 49, Height: 11})
	m.state = StateConfirm
	_, cmd = updateWithCmd(t, m, runeKey("q"))
	if !commandIsQuit(t, cmd) {
		t.Fatal("q did not match too-small hint")
	}
}

func TestHomepageInstallAvailabilityAndCompletion(t *testing.T) {
	opens := 0
	unavailable := New(uiPkgs(), false, false, true, Dependencies{
		Open: func(domain.Package) tea.Cmd {
			opens++
			return func() tea.Msg { return ActionResultMsg{Action: "Open homepage"} }
		},
		InstallUnavailable: "Homebrew is unavailable",
	})
	unavailable = update(t, unavailable, tea.WindowSizeMsg{Width: 100, Height: 20})
	var cmd tea.Cmd
	unavailable, cmd = updateWithCmd(t, unavailable, runeKey("o"))
	if opens != 1 || cmd == nil {
		t.Fatal("homepage action unavailable")
	}
	unavailable = update(t, unavailable, runeKey("i"))
	if unavailable.State() == StateConfirm || !strings.Contains(unavailable.View(), "Homebrew is unavailable") || !strings.Contains(unavailable.View(), "Install: Unavailable") {
		t.Fatal(unavailable.View())
	}

	runs := 0
	m := New(uiPkgs(), false, false, true, Dependencies{Install: func(domain.Package) tea.Cmd {
		runs++
		return nil
	}})
	m = update(t, m, runeKey("i"))
	if runs != 0 || m.State() != StateConfirm {
		t.Fatal()
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = update(t, m, runeKey("i"))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if runs != 1 || m.State() != StateRunningInstall {
		t.Fatal(runs, m.State())
	}
	m = update(t, m, platform.ExecResult{})
	if m.State() != StateBrowse || !strings.Contains(m.status, "complete") {
		t.Fatal(m.State(), m.status)
	}
}

func TestRefreshSummaryStaleFeedbackAndSelectionRetention(t *testing.T) {
	m := New(uiPkgs(), false, true, true, Dependencies{})
	m.width, m.height = 100, 20
	m.refreshing = true
	m.progress = "fetching"
	if header := m.header(); !strings.Contains(header, "STALE") || !strings.Contains(header, "refreshing") {
		t.Fatal(header)
	}
	m.selected = 1
	selected, _ := m.selectedPackage()
	replacement := append([]domain.Package(nil), uiPkgs()...)
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{
		Done:     true,
		Packages: replacement,
		Summary:  refresh.Summary{SkippedFormulae: 2, SkippedCasks: 3, Warning: "sync warning"},
	}})
	got, _ := m.selectedPackage()
	if got.Key() != selected.Key() {
		t.Fatalf("selection changed from %s to %s", selected.Key(), got.Key())
	}
	for _, want := range []string{"skipped 2 formulae, 3 casks", "sync warning"} {
		if !strings.Contains(m.status, want) {
			t.Fatal(m.status)
		}
	}
	if !strings.Contains(m.footer(), "↑/↓ move") || !strings.Contains(m.footer(), "sync warning") {
		t.Fatal(m.footer())
	}
}

func TestFatalRetryAndCombinedBootstrapDiagnostic(t *testing.T) {
	ch := make(chan RefreshEvent)
	m := New(nil, true, false, true, Dependencies{
		Refresh:             func() <-chan RefreshEvent { return ch },
		BootstrapDiagnostic: "invalid index preserved at backup.db",
	})
	m.width, m.height = 100, 20
	m.refreshing = true
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Err: errors.New("network down")}})
	if m.State() != StateFatal || !strings.Contains(m.status, "invalid index") || !strings.Contains(m.status, "network down") {
		t.Fatal(m.State(), m.status)
	}
	m.refreshing = false
	_, cmd := updateWithCmd(t, m, runeKey("r"))
	if cmd == nil {
		t.Fatal("fatal retry not queued")
	}
}
