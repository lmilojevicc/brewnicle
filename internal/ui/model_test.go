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
	caskTime := now.Add(-3 * time.Hour)
	fontTime := now.Add(-4 * time.Hour)
	return []domain.Package{
		{Name: "new", Kind: domain.KindFormula, Description: "needle", Homepage: "https://example.test/new", InstallTarget: "new", AddedAt: &now},
		{Name: "second", Kind: domain.KindFormula, Description: "other", Homepage: "https://example.test/second", InstallTarget: "second", AddedAt: &older},
		{Name: "app", Kind: domain.KindCask, Description: "desktop", InstallTarget: "app", AddedAt: &caskTime},
		{Name: "font-new", Kind: domain.KindFont, Description: "needle font", InstallTarget: "font-new", AddedAt: &fontTime},
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

func TestInitialIndexingElapsedAndPhaseRemainVisibleAndStopOnCompletion(t *testing.T) {
	startedAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := make(chan RefreshEvent)
	m := New(nil, true, false, true, Dependencies{Refresh: func() <-chan RefreshEvent { return ch }})
	m.now = func() time.Time { return startedAt }
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
	// Trigger the refresh directly rather than waiting for the startup frame.
	m.refreshing = false
	m.pendingRefresh = false
	m, cmd := updateWithCmd(t, m, startRefreshMsg{})
	if cmd == nil || !strings.Contains(m.View(), "elapsed 00:00") {
		t.Fatalf("bootstrap elapsed missing: %q", m.View())
	}
	m.now = func() time.Time { return startedAt.Add(7 * time.Second) }
	m = update(t, m, indexTickMsg{RunID: m.indexRunID})
	if got := m.View(); !strings.Contains(got, "elapsed 00:07") {
		t.Fatalf("elapsed did not advance: %q", got)
	}
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Progress: &refresh.Progress{
		Phase: refresh.PhaseHistory, Detail: "scanning complete history",
	}}})
	if got := m.View(); !strings.Contains(got, "indexing history") || !strings.Contains(got, "scanning complete history") {
		t.Fatalf("history stage is not visible in narrow bootstrap view: %q", got)
	}
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Err: errors.New("canceled")}})
	if m.Refreshing() || m.indexingElapsed() != 0 {
		t.Fatal("completed refresh retained active elapsed time")
	}
	_, staleCmd := updateWithCmd(t, m, indexTickMsg{RunID: m.indexRunID})
	if staleCmd != nil {
		t.Fatal("stale timer scheduled another tick after completion")
	}
}

func TestDefaultRangeMovementAndRangeKeys(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{})
	if m.Range() != domain.Range30D || m.KindFilter() != domain.KindFilterAll || len(m.Visible()) != 4 {
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

func TestKindFilterCyclingCompositionAndOwnership(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{})
	m = update(t, m, runeKey("j"))
	selected, _ := m.selectedPackage()
	m = update(t, m, runeKey("f"))
	if m.KindFilter() != domain.KindFilterFormula {
		t.Fatal(m.KindFilter())
	}
	if got, _ := m.selectedPackage(); got.Key() != selected.Key() {
		t.Fatalf("formula selection not retained: got %s want %s", got.Key(), selected.Key())
	}
	m = update(t, m, runeKey("f"))
	if m.KindFilter() != domain.KindFilterCask || len(m.Visible()) != 1 || m.Visible()[0].Name != "app" {
		t.Fatal(m.KindFilter(), m.Visible())
	}
	m = update(t, m, runeKey("F"))
	if m.KindFilter() != domain.KindFilterFormula || m.Visible()[0].Name != "new" {
		t.Fatal("reverse cycle or fallback", m.KindFilter(), m.Visible())
	}
	m = update(t, m, runeKey("F"))
	if m.KindFilter() != domain.KindFilterAll {
		t.Fatal(m.KindFilter())
	}
	m = update(t, m, runeKey("f"))
	m = update(t, m, runeKey("f"))
	m = update(t, m, runeKey("f"))
	if m.KindFilter() != domain.KindFilterFont || len(m.Visible()) != 1 {
		t.Fatal(m.KindFilter(), m.Visible())
	}
	m = update(t, m, runeKey("1"))
	if m.Range() != domain.Range7D || m.KindFilter() != domain.KindFilterFont || len(m.Visible()) != 1 {
		t.Fatal("range/type composition", m.Range(), m.KindFilter(), m.Visible())
	}
	m = update(t, m, runeKey("/"))
	m = update(t, m, runeKey("f"))
	if m.KindFilter() != domain.KindFilterFont || m.input.Value() != "f" {
		t.Fatal("search did not own f")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.Visible()) != 1 || m.Visible()[0].Kind != domain.KindFont {
		t.Fatal("kind/search AND composition", m.Visible())
	}
	m.input.SetValue("")
	m.applyFilter("")
	m.state = StateConfirm
	m = update(t, m, runeKey("F"))
	if m.State() != StateConfirm || m.KindFilter() != domain.KindFilterFont {
		t.Fatal("confirmation did not own F")
	}
	m.state = StateHelp
	m = update(t, m, runeKey("f"))
	if m.State() != StateHelp || m.KindFilter() != domain.KindFilterFont {
		t.Fatal("help did not own f")
	}
	m.state = StateBrowse
	selected, _ = m.selectedPackage()
	replacement := append([]domain.Package(nil), uiPkgs()...)
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Packages: replacement}})
	if got, _ := m.selectedPackage(); got.Key() != selected.Key() || m.KindFilter() != domain.KindFilterFont {
		t.Fatal("refresh did not retain kind filter and selection")
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 49, Height: 11})
	m = update(t, m, runeKey("f"))
	if m.KindFilter() != domain.KindFilterFont {
		t.Fatal("too-small layout did not own f")
	}
}

func TestNarrowDetailReturnsToListWhenTypeFilterHasNoMatches(t *testing.T) {
	now := time.Now().UTC()
	m := New([]domain.Package{{Name: "only-formula", Kind: domain.KindFormula, AddedAt: &now}}, false, false, true, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
	m.state = StateNarrowDetail
	m = update(t, m, runeKey("f")) // formula still matches
	if m.State() != StateNarrowDetail || len(m.Visible()) != 1 {
		t.Fatal("matching type filter should preserve detail", m.State(), m.Visible())
	}
	m = update(t, m, runeKey("f")) // cask has no matches
	if m.State() != StateBrowse || len(m.Visible()) != 0 {
		t.Fatal("empty type filter should return to list", m.State(), m.Visible())
	}
	view := m.View()
	if !strings.Contains(view, emptyResultsMessage) || !strings.Contains(view, "enter details") {
		t.Fatal(view)
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
	m = update(t, m, runeKey("desktop"))
	if len(m.Visible()) != 1 || !strings.Contains(m.View(), "desktop") {
		t.Fatal(m.Visible())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.State() != StateBrowse || len(m.Visible()) != 1 {
		t.Fatal("first escape must keep query")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.Visible()) != 4 || m.query != "" {
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
	if unavailable.State() == StateConfirm || unavailable.statusLevel != statusWarning || !strings.Contains(unavailable.View(), "Homebrew is unavailable") || !strings.Contains(unavailable.View(), "install    Unavailable") {
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
	if m.State() != StateBrowse || m.statusLevel != statusSuccess || !strings.Contains(m.status, "complete") {
		t.Fatal(m.State(), m.status)
	}
	m = update(t, m, platform.ExecResult{Err: errors.New("denied")})
	if m.statusLevel != statusError {
		t.Fatal("failed install did not receive error severity")
	}
}

func TestRefreshRemovalCancelsPinnedInstallConfirmation(t *testing.T) {
	packages := uiPkgs()[:2]
	runs := 0
	m := New(packages, false, false, true, Dependencies{Install: func(domain.Package) tea.Cmd {
		runs++
		return nil
	}})
	m = update(t, m, runeKey("i"))
	if m.State() != StateConfirm || !m.hasConfirmation || m.confirmPackage.Name != "new" {
		t.Fatal(m.State(), m.hasConfirmation, m.confirmPackage)
	}
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Packages: packages[1:]}})
	if m.State() != StateBrowse || m.hasConfirmation || runs != 0 || !strings.Contains(m.status, "confirmation canceled") || !strings.Contains(m.status, "new") {
		t.Fatal(m.State(), m.hasConfirmation, runs, m.status)
	}
	m = update(t, m, runeKey("y"))
	if runs != 0 {
		t.Fatal("removed package was installed")
	}
}

func TestRefreshReorderingKeepsPinnedInstallTarget(t *testing.T) {
	packages := uiPkgs()[:2]
	var installed domain.Package
	m := New(packages, false, false, true, Dependencies{Install: func(pkg domain.Package) tea.Cmd {
		installed = pkg
		return nil
	}})
	m = update(t, m, runeKey("i"))
	reordered := []domain.Package{packages[1], packages[0]}
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Packages: reordered}})
	if m.State() != StateConfirm || !m.hasConfirmation || m.confirmPackage.Key() != packages[0].Key() {
		t.Fatal(m.State(), m.hasConfirmation, m.confirmPackage)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.State() != StateRunningInstall || installed.Key() != packages[0].Key() || installed.Key() == reordered[0].Key() {
		t.Fatal(m.State(), installed)
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
	m.rangeValue = domain.RangeAll
	m.kindFilter = domain.KindFilterFormula
	m.input.SetValue("other")
	m.applyFilter("")
	selected, _ := m.selectedPackage()
	replacement := append([]domain.Package(nil), uiPkgs()...)
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{
		Done:     true,
		Packages: replacement,
		Summary:  refresh.Summary{SkippedFormulae: 2, SkippedCasks: 3, Warning: "sync warning"},
	}})
	got, _ := m.selectedPackage()
	if got.Key() != selected.Key() || m.rangeValue != domain.RangeAll || m.kindFilter != domain.KindFilterFormula || m.query != "other" {
		t.Fatalf("refresh changed UI state: selected %s->%s range=%s kind=%s query=%q", selected.Key(), got.Key(), m.rangeValue, m.kindFilter, m.query)
	}
	if m.statusLevel != statusWarning {
		t.Fatal("skipped refresh did not receive warning severity")
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
	if m.State() != StateFatal || m.statusLevel != statusError || !strings.Contains(m.status, "invalid index") || !strings.Contains(m.status, "network down") {
		t.Fatal(m.State(), m.status)
	}
	m.refreshing = false
	_, cmd := updateWithCmd(t, m, runeKey("r"))
	if cmd == nil {
		t.Fatal("fatal retry not queued")
	}
}

func provisionalPackages() []domain.Package {
	return []domain.Package{
		{Name: "alpha", Kind: domain.KindFormula, Description: "tool package", Homepage: "https://example.test/alpha", InstallTarget: "alpha"},
		{Name: "beta", Kind: domain.KindFormula, Description: "tool package", Homepage: "https://example.test/beta", InstallTarget: "beta"},
		{Name: "desktop", Kind: domain.KindCask, Description: "desktop app", InstallTarget: "desktop"},
	}
}

func TestProvisionalCatalogBrowsesAllAndDisablesBoundedDateRanges(t *testing.T) {
	m := New(nil, true, false, true, Dependencies{})
	m.width, m.height = 100, 20
	m.refreshing = true
	m.refreshCh = make(chan RefreshEvent)
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{
		CatalogReady: true,
		Packages:     provisionalPackages(),
		Progress:     &refresh.Progress{Phase: refresh.PhaseCatalogReady, Detail: "indexing exact dates"},
	}})
	if m.State() != StateBrowse || m.Dates() != DatesIndexing || m.Range() != domain.RangeAll || len(m.Visible()) != 3 {
		t.Fatal(m.State(), m.Dates(), m.Range(), m.Visible())
	}
	m = update(t, m, runeKey("1"))
	if m.Range() != domain.RangeAll || len(m.Visible()) != 3 || !strings.Contains(m.status, "available after") {
		t.Fatal(m.Range(), m.Visible(), m.status)
	}
	view := m.View()
	for _, want := range []string{"DATES INDEXING", "Date indexing", "alpha"} {
		if !strings.Contains(view, want) {
			t.Fatalf("provisional view missing %q:\n%s", want, view)
		}
	}
}

func TestWinningSnapshotRemainsBrowsableAfterCatalogFailure(t *testing.T) {
	winner := uiPkgs()[:1]
	m := New(nil, true, false, true, Dependencies{})
	m.width, m.height = 100, 20
	m.refreshing = true
	m.refreshCh = make(chan RefreshEvent)
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{SnapshotReady: true, Packages: winner}})
	if m.State() != StateBrowse || m.Dates() != DatesReady || len(m.Visible()) != 1 || m.Visible()[0].Name != "new" {
		t.Fatal(m.State(), m.Dates(), m.Visible())
	}
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Err: errors.New("catalog down")}})
	if m.State() != StateBrowse || m.Dates() != DatesReady || len(m.Visible()) != 1 || !strings.Contains(m.status, "Refresh failed") {
		t.Fatal(m.State(), m.Dates(), m.Visible(), m.status)
	}
}

func TestProvisionalFailureRetainsBrowsingAndRetry(t *testing.T) {
	starts := 0
	retryCh := make(chan RefreshEvent)
	m := New(nil, true, false, true, Dependencies{Refresh: func() <-chan RefreshEvent {
		starts++
		return retryCh
	}})
	m.width, m.height = 100, 20
	m.refreshing = true
	m.refreshCh = make(chan RefreshEvent)
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{CatalogReady: true, Packages: provisionalPackages()}})
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Err: errors.New("scan failed")}})
	if m.State() != StateBrowse || m.Dates() != DatesUnavailable || len(m.Visible()) != 3 {
		t.Fatal(m.State(), m.Dates(), m.Visible())
	}
	for _, want := range []string{"DATES UNAVAILABLE", "Date unavailable", "press r to retry"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("failed provisional view missing %q:\n%s", want, m.View())
		}
	}
	m, startCmd := updateWithCmd(t, m, runeKey("r"))
	if startCmd == nil {
		t.Fatal("retry was not queued")
	}
	m, waitCmd := updateWithCmd(t, m, startCmd())
	if starts != 1 || waitCmd == nil || !m.Refreshing() || m.Dates() != DatesIndexing || len(m.Visible()) != 3 {
		t.Fatal(starts, m.Refreshing(), m.Dates(), m.Visible())
	}
}

func TestExactCompletionPreservesProvisionalSearchTypeAndSelection(t *testing.T) {
	m := New(nil, true, false, true, Dependencies{})
	m.width, m.height = 100, 20
	m.refreshing = true
	m.refreshCh = make(chan RefreshEvent)
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{CatalogReady: true, Packages: provisionalPackages()}})
	m = update(t, m, runeKey("f"))
	m = update(t, m, runeKey("j"))
	m = update(t, m, runeKey("/"))
	m = update(t, m, runeKey("tool"))
	selected, ok := m.selectedPackage()
	if !ok || selected.Name != "beta" {
		t.Fatal(selected, ok)
	}
	now := time.Now().UTC()
	exact := provisionalPackages()
	for i := range exact {
		at := now.Add(-time.Duration(i+1) * time.Hour)
		exact[i].AddedAt = &at
	}
	m = update(t, m, refreshEventMsg{Event: RefreshEvent{Done: true, Packages: exact}})
	got, ok := m.selectedPackage()
	if !ok || got.Key() != selected.Key() || m.State() != StateSearch || m.KindFilter() != domain.KindFilterFormula || m.query != "tool" || m.Dates() != DatesReady {
		t.Fatal(got, ok, m.State(), m.KindFilter(), m.query, m.Dates())
	}
}
