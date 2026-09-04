package ui

import (
	"fmt"
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

func TestStylesUseOnlyApprovedTerminalPalette(t *testing.T) {
	approved := map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "8": true}
	for _, noColor := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "no-color"}[noColor], func(t *testing.T) {
			s := makeStyles(noColor)
			styles := map[string]lipgloss.Style{
				"title": s.title, "accent": s.accent, "selected": s.selected,
				"formula": s.formula, "cask": s.cask, "font": s.font,
				"success": s.success, "warning": s.warning, "danger": s.danger,
				"muted": s.muted, "link": s.link, "install prompt": s.installPrompt,
			}
			m := New(uiPkgs(), false, false, noColor, Dependencies{})
			styles["input prompt"] = m.input.PromptStyle
			styles["input text"] = m.input.TextStyle
			styles["input placeholder"] = m.input.PlaceholderStyle
			styles["input completion"] = m.input.CompletionStyle
			styles["input cursor legacy"] = m.input.CursorStyle
			styles["input cursor text"] = m.input.Cursor.TextStyle
			styles["input cursor"] = m.input.Cursor.Style
			for name, style := range styles {
				assertPaletteStyle(t, name, style, noColor, approved)
			}
			if !s.title.GetBold() || !s.accent.GetBold() || !s.accent.GetUnderline() || !s.selected.GetReverse() {
				t.Fatal("attribute and text fallbacks for title, active range, or selection are missing")
			}
			if !m.input.Cursor.Style.GetReverse() {
				t.Fatal("cursor lost its color-independent reverse cue")
			}
			m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
			view := m.View()
			if !strings.Contains(view, "›") || !strings.Contains(view, "[30d]") {
				t.Fatal("text selection cues missing")
			}
		})
	}
}

func assertPaletteStyle(t *testing.T, name string, style lipgloss.Style, noColor bool, approved map[string]bool) {
	t.Helper()
	if _, ok := style.GetBackground().(lipgloss.NoColor); !ok {
		t.Errorf("%s paints a background with %T", name, style.GetBackground())
	}
	foreground := style.GetForeground()
	if _, ok := foreground.(lipgloss.NoColor); ok {
		return
	}
	color, ok := foreground.(lipgloss.Color)
	if noColor || !ok || !approved[string(color)] {
		t.Errorf("%s uses unapproved foreground %#v", name, foreground)
	}
}

func TestVividRoleMapping(t *testing.T) {
	s := makeStyles(false)
	for name, tc := range map[string]struct {
		style lipgloss.Style
		want  string
	}{
		"title": {s.title, "5"}, "accent": {s.accent, "6"}, "selected": {s.selected, "6"},
		"formula": {s.formula, "4"}, "cask": {s.cask, "5"}, "font": {s.font, "3"},
		"success": {s.success, "2"}, "warning": {s.warning, "3"}, "danger": {s.danger, "1"},
		"muted": {s.muted, "8"}, "link": {s.link, "6"}, "install": {s.installPrompt, "3"},
	} {
		color, ok := tc.style.GetForeground().(lipgloss.Color)
		if !ok || string(color) != tc.want {
			t.Errorf("%s foreground = %#v, want ANSI %s", name, tc.style.GetForeground(), tc.want)
		}
	}
	m := New(uiPkgs(), false, false, false, Dependencies{})
	for name, style := range map[string]lipgloss.Style{"input prompt": m.input.PromptStyle, "input cursor": m.input.Cursor.Style} {
		color, ok := style.GetForeground().(lipgloss.Color)
		if !ok || string(color) != "6" {
			t.Errorf("%s foreground = %#v, want ANSI 6", name, style.GetForeground())
		}
	}
	for name, style := range map[string]lipgloss.Style{
		"input text": m.input.TextStyle, "input placeholder": m.input.PlaceholderStyle,
		"input completion": m.input.CompletionStyle, "input cursor text": m.input.Cursor.TextStyle,
	} {
		if _, ok := style.GetForeground().(lipgloss.NoColor); !ok {
			t.Errorf("%s should inherit terminal foreground", name)
		}
	}
}

func TestRowsCenterSelectionWhenPossible(t *testing.T) {
	packages := make([]domain.Package, 10)
	for i := range packages {
		packages[i] = domain.Package{Name: fmt.Sprintf("p%02d", i), Kind: domain.KindFormula}
	}
	for _, tc := range []struct {
		name, wantName                      string
		count, height, selected, markerLine int
	}{
		{"first clamps", "p00", 10, 5, 0, 0},
		{"odd middle", "p05", 10, 5, 5, 2},
		{"last clamps", "p09", 10, 5, 9, 4},
		{"even lower middle", "p05", 10, 4, 5, 2},
		{"fewer rows", "p01", 3, 5, 1, 1},
		{"equal rows", "p04", 5, 5, 4, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, false, false, true, Dependencies{})
			m.visible = packages[:tc.count]
			m.selected = tc.selected
			rows := m.rows(40, tc.height)
			if len(rows) != min(tc.count, tc.height) {
				t.Fatalf("rows=%d want %d", len(rows), min(tc.count, tc.height))
			}
			for i, row := range rows {
				if strings.Contains(row, "›") {
					if i != tc.markerLine || !strings.Contains(row, tc.wantName) {
						t.Fatalf("selected row %d %q want line %d package %s", i, row, tc.markerLine, tc.wantName)
					}
					return
				}
			}
			t.Fatal("no selected marker")
		})
	}
	m := New(nil, false, false, true, Dependencies{})
	if rows := m.rows(40, 5); len(rows) != 1 || rows[0] != emptyResultsMessage {
		t.Fatal(rows)
	}
	if rows := m.rows(40, 0); rows != nil {
		t.Fatal(rows)
	}
}

func TestKindFilterHeaderFooterHelpAndRole(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
	m = update(t, m, runeKey("f"))
	for _, want := range []string{"type:formula", "f type"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("missing %q:\n%s", want, m.View())
		}
	}
	m = update(t, m, runeKey("?"))
	help := m.View()
	for _, want := range []string{"f/F  package type forward/back", "? or esc  close help", "q  quit"} {
		if !strings.Contains(help, want) {
			t.Fatalf("minimum-size help missing %q:\n%s", want, help)
		}
	}
	if lines := strings.Split(help, "\n"); len(lines) > NarrowMinHeight {
		t.Fatalf("minimum-size help has %d lines", len(lines))
	}
	if got := makeStyles(false).kindFilter(domain.KindFilterFormula).GetForeground(); got != lipgloss.Color("4") {
		t.Fatalf("formula filter role = %#v", got)
	}
	if got := makeStyles(false).kindFilter(domain.KindFilterAll).GetForeground(); got != lipgloss.Color("6") {
		t.Fatalf("all filter role = %#v", got)
	}
	if _, ok := makeStyles(true).kindFilter(domain.KindFilterFont).GetForeground().(lipgloss.NoColor); !ok {
		t.Fatal("NO_COLOR kind filter has color")
	}
}

func TestEmptyStateShowsEveryRecoveryAction(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 16}, {Width: 50, Height: 12}} {
		m := New([]domain.Package{{Name: "unknown", Kind: domain.KindFormula}}, false, false, true, Dependencies{})
		m = update(t, m, size)
		view := m.View()
		if !strings.Contains(view, emptyResultsMessage) || strings.Contains(view, "No matches:") && strings.Contains(view, "…") {
			t.Fatalf("%+v clipped recovery copy:\n%s", size, view)
		}
		for _, want := range []string{"/ search", "f type", "5 all"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%+v missing recovery %q:\n%s", size, want, view)
			}
		}
	}
}

func TestFullViewsCenterSelectionUsingActualBodyCapacity(t *testing.T) {
	packages := make([]domain.Package, 30)
	for i := range packages {
		packages[i] = domain.Package{Name: fmt.Sprintf("p%02d", i), Kind: domain.KindFormula}
	}
	for _, tc := range []struct {
		name                     string
		width, height            int
		headerLines, footerLines int
		configure                func(*Model)
	}{
		{name: "wide one-line header and footer", width: 100, height: 20, headerLines: 1, footerLines: 1},
		{name: "narrow two-line header and footer", width: 50, height: 12, headerLines: 2, footerLines: 2, configure: func(m *Model) {
			m.state = StateSearch
			m.input.Focus()
			m.stale = true
			m.refreshing = true
			m.setStatus("status", statusWarning)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, false, false, true, Dependencies{})
			m = update(t, m, tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			m.packages, m.visible, m.selected = packages, packages, 15
			if tc.configure != nil {
				tc.configure(&m)
			}
			header, footer := m.headerLines(), m.footerLines()
			if len(header) != tc.headerLines || len(footer) != tc.footerLines {
				t.Fatalf("header/footer = %d/%d want %d/%d", len(header), len(footer), tc.headerLines, tc.footerLines)
			}
			bodyHeight := tc.height - len(header) - len(footer)
			wantLine := len(header) + min(bodyHeight, len(packages))/2
			gotLine := -1
			for i, line := range strings.Split(m.View(), "\n") {
				if strings.Contains(line, "›") {
					gotLine = i
					break
				}
			}
			if gotLine != wantLine {
				t.Fatalf("selected marker line = %d want %d; body height %d\n%s", gotLine, wantLine, bodyHeight, m.View())
			}
		})
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
	for _, line := range detailLines(p, 8, uiPkgs()[0].UpdatedAt, false, DatesReady, makeStyles(true)) {
		if lipgloss.Width(line) > 8 {
			t.Fatalf("detail line too wide: %q", line)
		}
	}
}
