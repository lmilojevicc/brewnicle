package ui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/milo/brewnicle/internal/domain"
)

type State int

type statusLevel int

const (
	statusNone statusLevel = iota
	statusSuccess
	statusWarning
	statusError
)

const (
	StateBootstrap State = iota
	StateBrowse
	StateSearch
	StateHelp
	StateConfirm
	StateRunningInstall
	StateNarrowDetail
	StateFatal
)

type Dependencies struct {
	Refresh             RefreshStarter
	Open                func(domain.Package) tea.Cmd
	Install             func(domain.Package) tea.Cmd
	InstallUnavailable  string
	BootstrapDiagnostic string
}

type Model struct {
	packages, visible   []domain.Package
	rangeValue          domain.Range
	query               string
	selected            int
	width, height       int
	state               State
	previous            State
	input               textinput.Model
	deps                Dependencies
	pendingRefresh      bool
	startupFramePending bool
	refreshing          bool
	refreshCh           <-chan RefreshEvent
	progress            string
	status              string
	statusLevel         statusLevel
	stale               bool
	bootstrapDiagnostic string
	now                 func() time.Time
	noColor             bool
}

func New(packages []domain.Package, bootstrap, stale, noColor bool, deps Dependencies) Model {
	in := textinput.New()
	inherited := lipgloss.NewStyle()
	in.PromptStyle = inherited
	in.TextStyle = inherited
	in.PlaceholderStyle = inherited
	in.CompletionStyle = inherited
	in.CursorStyle = inherited
	in.Cursor.TextStyle = inherited
	in.Cursor.Style = lipgloss.NewStyle().Reverse(true)
	if !noColor {
		focus := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		in.PromptStyle = focus
		in.CursorStyle = focus.Reverse(true)
		in.Cursor.Style = focus.Reverse(true)
	}
	in.Prompt = "Search: "
	in.CharLimit = 120
	in.Width = 30
	state := StateBrowse
	if bootstrap {
		state = StateBootstrap
	}
	m := Model{
		packages:            packages,
		rangeValue:          domain.DefaultRange,
		state:               state,
		input:               in,
		deps:                deps,
		pendingRefresh:      bootstrap || stale,
		stale:               stale,
		bootstrapDiagnostic: deps.BootstrapDiagnostic,
		now:                 time.Now,
		noColor:             noColor,
	}
	m.applyFilter("")
	return m
}

// Init deliberately starts no network or Git work. The first usable
// WindowSizeMsg schedules a short frame boundary; refresh is queued only after
// the renderer has had time to flush the bootstrap/cached view.
func (m Model) Init() tea.Cmd { return nil }

func (m *Model) applyFilter(keep string) {
	m.query = m.input.Value()
	old := keep
	if old == "" && m.selected >= 0 && m.selected < len(m.visible) {
		old = m.visible[m.selected].Key()
	}
	m.visible = domain.Filter(m.packages, m.rangeValue, m.query, m.now())
	m.selected = 0
	for i, p := range m.visible {
		if p.Key() == old {
			m.selected = i
			break
		}
	}
}

func (m *Model) setStatus(text string, level statusLevel) {
	m.status = text
	m.statusLevel = level
}

func (m Model) selectedPackage() (domain.Package, bool) {
	if m.selected < 0 || m.selected >= len(m.visible) {
		return domain.Package{}, false
	}
	return m.visible[m.selected], true
}

func (m Model) Range() domain.Range       { return m.rangeValue }
func (m Model) Refreshing() bool          { return m.refreshing }
func (m Model) PendingRefresh() bool      { return m.pendingRefresh }
func (m Model) Stale() bool               { return m.stale }
func (m Model) State() State              { return m.state }
func (m Model) Visible() []domain.Package { return append([]domain.Package(nil), m.visible...) }
