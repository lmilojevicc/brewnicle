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
	for _, line := range detailLines(p, 8, uiPkgs()[0].UpdatedAt, false, makeStyles(true)) {
		if lipgloss.Width(line) > 8 {
			t.Fatalf("detail line too wide: %q", line)
		}
	}
}
