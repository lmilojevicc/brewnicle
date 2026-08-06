package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/milo/brewnicle/internal/domain"
)

func TestResponsiveViewsFitTerminalCellWidth(t *testing.T) {
	packages := uiPkgs()
	packages[0].Name = "界e\u0301-wide-name"
	packages[0].Description = "界界 combining e\u0301 description"
	for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 20}, {Width: 60, Height: 15}, {Width: 49, Height: 11}} {
		m := New(packages, false, false, true, Dependencies{})
		m = update(t, m, size)
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) > size.Height {
			t.Errorf("%+v height %d", size, len(lines))
		}
		for _, line := range lines {
			if width := lipgloss.Width(line); width > size.Width {
				t.Errorf("%+v line width %d: %q", size, width, line)
			}
		}
	}
}

func TestNoColorHasNoEscape(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	if strings.Contains(m.View(), "\x1b[3") {
		t.Fatal("color escape")
	}
}

func TestBreakpointsAndNarrowHints(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{Install: func(domain.Package) tea.Cmd { return nil }})
	m = update(t, m, tea.WindowSizeMsg{Width: 49, Height: 12})
	if !strings.Contains(m.View(), "too small") {
		t.Fatal()
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
	if strings.Contains(m.View(), "too small") || !strings.Contains(m.View(), "enter details") {
		t.Fatal(m.View())
	}
	m.state = StateNarrowDetail
	if !strings.Contains(m.View(), "enter back") {
		t.Fatal(m.View())
	}
}

func TestMinimumWidthSearchKeepsStateIndicators(t *testing.T) {
	m := New(uiPkgs(), false, true, true, Dependencies{Install: func(domain.Package) tea.Cmd { return nil }})
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
	m.refreshing = true
	m.progress = "fetching a long repository description"
	m = update(t, m, runeKey("/"))
	view := m.View()
	for _, want := range []string{"Search:", "STALE", "refreshing", "enter apply"} {
		if !strings.Contains(view, want) {
			t.Fatalf("minimum-width search lost %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 50 {
			t.Fatalf("line width %d exceeds 50: %q", width, line)
		}
	}
}

func TestInstallUnavailableDetailDoesNotHideHomepage(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{InstallUnavailable: "Homebrew unavailable"})
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	view := m.View()
	if !strings.Contains(view, "Install: Unavailable") || !strings.Contains(view, "Homepage: https://example.test/new") {
		t.Fatal(view)
	}
}

func TestTruncateAndWrapUseCellWidth(t *testing.T) {
	for _, value := range []string{"界界界", "e\u0301e\u0301e\u0301", "abc界def"} {
		if got := truncate(value, 4); lipgloss.Width(got) > 4 {
			t.Fatalf("truncate %q => %q width %d", value, got, lipgloss.Width(got))
		}
	}
	p := domain.Package{Name: "界", Kind: domain.KindFormula, Description: "界界 e\u0301e\u0301", InstallTarget: "界"}
	for _, line := range detailLines(p, 8, uiPkgs()[0].UpdatedAt, false) {
		if lipgloss.Width(line) > 8 {
			t.Fatalf("detail line too wide: %q", line)
		}
	}
}
