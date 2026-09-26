package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

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
	neutrals := map[string]bool{borderNeutral: true}
	for _, noColor := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "no-color"}[noColor], func(t *testing.T) {
			s := makeStyles(noColor)
			styles := map[string]lipgloss.Style{
				"title": s.title, "accent": s.accent, "selected": s.selected,
				"formula": s.formula, "cask": s.cask, "font": s.font,
				"success": s.success, "warning": s.warning, "danger": s.danger,
				"muted": s.muted, "link": s.link, "install prompt": s.installPrompt,
				"border": s.border,
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
				allowed := approved
				// The documented neutral ramp is only permitted for the structural
				// border style; semantic roles stay on ANSI 1-8.
				if name == "border" {
					allowed = neutrals
				}
				assertPaletteStyle(t, name, style, noColor, allowed)
			}
			if !noColor {
				if got := s.border.GetForeground(); got != lipgloss.Color(borderNeutral) {
					t.Errorf("border foreground = %#v, want ANSI-256 %s", got, borderNeutral)
				}
			} else if _, ok := s.border.GetForeground().(lipgloss.NoColor); !ok {
				t.Errorf("border kept a foreground under NO_COLOR: %#v", s.border.GetForeground())
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
		// The footer repeats the "/ search" and "f type" hints, so only the full
		// recovery row proves the row itself still carries every action; a
		// truncated row would otherwise pass on footer substrings alone.
		if !strings.Contains(view, emptyResultsMessage) {
			t.Fatalf("%+v lost the full recovery row:\n%s", size, view)
		}
		if strings.Contains(view, "…") {
			t.Fatalf("%+v elided recovery copy:\n%s", size, view)
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
		{name: "wide three-line header and one-line footer", width: 100, height: 20, headerLines: 3, footerLines: 1},
		{name: "narrow three-line header and one-line footer", width: 50, height: 12, headerLines: 3, footerLines: 1, configure: func(m *Model) {
			m.stale = true
			m.refreshing = true
			m.setStatus("status", statusWarning)
		}},
		{name: "narrow search four-line header and one-line footer", width: 50, height: 12, headerLines: 4, footerLines: 1, configure: func(m *Model) {
			m.state = StateSearch
			m.stale = true
			m.refreshing = true
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
			// The body panel spends one line on its top border and one on its
			// bottom border in addition to the header box and footer.
			bodyHeight := tc.height - len(header) - len(footer) - bodyBorderLines
			wantLine := len(header) + 1 + min(bodyHeight, len(packages))/2
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
	if !strings.Contains(view, "install    Unavailable") || !strings.Contains(view, "homepage   https://example.test/new") {
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
	for _, line := range detailLines(p, 8, 100, uiPkgs()[0].UpdatedAt, false, DatesReady, makeStyles(true)) {
		if lipgloss.Width(line) > 8 {
			t.Fatalf("detail line too wide: %q", line)
		}
	}
}

func TestCompactAgeNeverExceedsThreeCells(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	days := func(d int) *time.Time {
		at := now.Add(-time.Duration(d) * 24 * time.Hour)
		return &at
	}
	for _, tc := range []struct {
		name string
		at   *time.Time
		want string
	}{
		{"nil date", nil, "?"},
		{"zero", days(0), "0d"},
		{"one", days(1), "1d"},
		{"99", days(99), "99d"},
		{"100", days(100), "3mo"},
		{"299", days(299), "9mo"},
		{"300", days(300), "1y"},
		{"364", days(364), "1y"},
		{"365", days(365), "1y"},
		{"1000", days(1000), "2y"},
		{"36500", days(36500), "99y"},
		{"100000", days(100000), "99y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := compactAge(tc.at, now, DatesReady)
			if got != tc.want {
				t.Fatalf("compactAge(%s) = %q want %q", tc.name, got, tc.want)
			}
			if width := lipgloss.Width(got); width > ageCells {
				t.Fatalf("compactAge(%s) = %q is %d cells", tc.name, got, width)
			}
		})
	}
	if got := compactAge(days(5), now, DatesIndexing); got != "?" {
		t.Fatalf("indexing date = %q want ?", got)
	}
	if got := compactAge(days(5), now, DatesUnavailable); got != "?" {
		t.Fatalf("unavailable date = %q want ?", got)
	}
}

// TestRowsPadEveryBranchToExactPaneWidth pins the row grammar against the
// medium and compact fallbacks that used to pad only downwards and overflow by
// up to four cells.
func TestRowsPadEveryBranchToExactPaneWidth(t *testing.T) {
	now := time.Now().UTC()
	added := now.Add(-10 * 24 * time.Hour)
	packages := []domain.Package{
		{Name: "kubernetes-cli", Kind: domain.KindFormula, Description: "Kubernetes CLI", InstallTarget: "kubernetes-cli", AddedAt: &added},
		{Name: "font-jetbrains-mono-nerd-font", Kind: domain.KindFont, Description: "Short", InstallTarget: "font-jetbrains-mono-nerd-font", AddedAt: &added},
		{Name: "jq", Kind: domain.KindCask, InstallTarget: "jq", AddedAt: &added},
	}
	m := New(packages, false, false, true, Dependencies{})
	m.visible = packages
	for _, width := range []int{26, 27, 28, 29, 37, 48, 63} {
		for _, selected := range []int{0, 1, 2} {
			m.selected = selected
			rows := m.rows(width, 10)
			if len(rows) != len(packages) {
				t.Fatalf("rows(%d) produced %d rows", width, len(rows))
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != width {
					t.Fatalf("rows(%d) selected %d row %d width %d want %d: %q", width, selected, i, got, width, row)
				}
			}
		}
	}
	// A single name wider than the pane is the only unavoidable truncation.
	m.visible = []domain.Package{{Name: "font-jetbrains-mono-nerd-font", Kind: domain.KindFont}}
	for _, width := range []int{8, 20, 29} {
		row := m.rows(width, 1)[0]
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("narrow rows(%d) width %d: %q", width, got, row)
		}
	}
}

func TestLongRefreshProgressCapsDetailBeforeHeaderIndicators(t *testing.T) {
	m := New(uiPkgs(), false, true, false, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	m.refreshing = true
	m.dates = DatesIndexing
	m.progress = strings.Repeat("indexing exact package dates ", 8)
	lines := strings.Split(m.header(), "\n")
	if len(lines) != 3 {
		t.Fatalf("header has %d lines:\n%s", len(lines), m.header())
	}
	cell := strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(lines[1], "│"), "│"), " ")
	if got := lipgloss.Width(cell); got != 88 {
		t.Fatalf("header content width %d want 88: %q", got, cell)
	}
	// Reconstruct the whole right cluster from the rendered cells: the count is
	// its first part, so everything from "4/5" to the end must survive.
	idx := strings.Index(cell, "4/5")
	if idx < 0 {
		t.Fatalf("count missing from header:\n%s", m.header())
	}
	if !strings.Contains(cell[:idx], "brewnicle") {
		t.Fatalf("brand lost from header: %q", cell)
	}
	cluster := cell[idx:]
	for _, want := range []string{"4/5", "STALE", "DATES INDEXING", "refreshing: "} {
		if !strings.Contains(cluster, want) {
			t.Fatalf("cluster lost %q: %q", want, cluster)
		}
	}
	prefix := "refreshing: "
	shown := strings.TrimRight(cluster[strings.Index(cluster, prefix)+len(prefix):], " ")
	if shown == m.progress {
		t.Fatalf("progress detail was not capped: %q", shown)
	}
	if !strings.HasSuffix(shown, "…") || !strings.HasPrefix(m.progress, strings.TrimSuffix(shown, "…")) {
		t.Fatalf("progress detail %q is not a capped prefix of %q", shown, m.progress)
	}
	for _, line := range lines {
		if width := lipgloss.Width(line); width > 90 {
			t.Fatalf("header line width %d: %q", width, line)
		}
	}
}

func TestColoredViewsFitTerminalWidth(t *testing.T) {
	packages := uiPkgs()
	packages[0].Name = "font-jetbrains-mono-nerd-font"
	packages[0].Kind = domain.KindFont
	install := func(domain.Package) tea.Cmd { return nil }
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 24}, {Width: 90, Height: 24}, {Width: 60, Height: 15}, {Width: 50, Height: 12}} {
		m := New(packages, false, true, false, Dependencies{Install: install})
		m = update(t, m, size)
		m.refreshing = true
		m.progress = "fetching a long repository description that would otherwise clip the header"
		m.dates = DatesUnavailable
		m.setStatus("a status message long enough to survive the footer join budget", statusWarning)
		for name, mutate := range map[string]func(*Model){
			"browse":        func(*Model) {},
			"search":        func(m *Model) { m.state = StateSearch },
			"narrow detail": func(m *Model) { m.state = StateNarrowDetail },
			"help":          func(m *Model) { m.state = StateHelp },
			"confirm": func(m *Model) {
				m.state = StateConfirm
				m.confirmPackage = packages[0]
				m.hasConfirmation = true
			},
		} {
			state := m
			mutate(&state)
			for _, line := range strings.Split(state.View(), "\n") {
				if width := lipgloss.Width(line); width > size.Width {
					t.Fatalf("%+v %s line width %d > %d: %q", size, name, width, size.Width, line)
				}
			}
		}
	}
}

func TestConfirmViewWrapsLongCommandWithoutTruncating(t *testing.T) {
	added := time.Now().UTC().Add(-24 * time.Hour)
	long := domain.Package{Name: "font-jetbrains-mono-nerd-font", Kind: domain.KindFont, InstallTarget: "font-jetbrains-mono-nerd-font", AddedAt: &added}
	m := New([]domain.Package{long}, false, false, true, Dependencies{Install: func(domain.Package) tea.Cmd { return nil }})
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
	m = update(t, m, runeKey("i"))
	view := m.View()
	if strings.Contains(view, "…") {
		t.Fatalf("confirmation command was truncated:\n%s", view)
	}
	// The command is the only multi-line block between the title and the
	// controls, so the displayed two-line reconstruction must match exactly.
	want := []string{"brew install --cask", "font-jetbrains-mono-nerd-font"}
	var got []string
	collecting := false
	for _, line := range strings.Split(view, "\n") {
		cell := strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(line, "│"), "│"), " ")
		switch {
		case strings.Contains(cell, "Confirm installation"):
			collecting = true
		case strings.HasPrefix(cell, "Enter/y install"):
			collecting = false
		case collecting && cell != "":
			got = append(got, cell)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("confirmation command = %q want %q:\n%s", got, want, view)
	}
	if rebuilt := strings.Join(got, " "); rebuilt != platformDisplay(long) {
		t.Fatalf("confirmation command rebuilt as %q want %q", rebuilt, platformDisplay(long))
	}
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 50 {
			t.Fatalf("confirmation line width %d: %q", width, line)
		}
	}
}

func TestDetailHeadingNeverTruncatesLongNames(t *testing.T) {
	s := makeStyles(true)
	p := domain.Package{Name: "font-jetbrains-mono-nerd-font", Kind: domain.KindFont}
	heading := detailHeading(p, 48, s)
	joined := strings.Join(heading, "")
	if strings.Contains(joined, "…") || !strings.Contains(joined, "font-jetbrains-mono-nerd-font") {
		t.Fatalf("heading truncated a name: %q", heading)
	}
	for _, line := range heading {
		if width := lipgloss.Width(line); width > 48 {
			t.Fatalf("heading line width %d: %q", width, line)
		}
	}
	if !strings.Contains(joined, "[font]") {
		t.Fatalf("short-enough name lost its kind suffix: %q", heading)
	}
	// A name wider than the pane wraps rather than truncating.
	narrow := detailHeading(p, 12, s)
	if len(narrow) < 3 {
		t.Fatalf("long name did not wrap: %q", narrow)
	}
	for _, line := range narrow {
		if width := lipgloss.Width(line); width > 12 {
			t.Fatalf("wrapped heading line width %d: %q", width, line)
		}
	}
}

func TestNarrowMinDetailKeepsHomepageAndInstall(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	added := now.Add(-10 * 24 * time.Hour)
	base := func(desc string) domain.Package {
		return domain.Package{Name: "font-maple", Kind: domain.KindFont, Description: desc, InstallTarget: "font-maple", AddedAt: &added}
	}
	for _, tc := range []struct{ name, desc string }{
		{"one-line description", "Rounded monospace programming font"},
		{"two-line description", "Rounded monospace programming font for code editing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base(tc.desc)
			m := New([]domain.Package{p}, false, false, true, Dependencies{Install: func(domain.Package) tea.Cmd { return nil }})
			m.now = func() time.Time { return now }
			m.applyFilter("")
			m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
			m.state = StateNarrowDetail
			view := m.View()
			if !strings.Contains(view, "homepage   Unavailable") {
				t.Fatalf("homepage missing:\n%s", view)
			}
			if !strings.Contains(view, "install    brew install --cask font-maple") {
				t.Fatalf("install missing:\n%s", view)
			}
			if lines := strings.Split(view, "\n"); len(lines) != 12 {
				t.Fatalf("view has %d lines", len(lines))
			}
		})
	}
}

// TestSearchHeaderFitsRightClusterOnOneLine guards the field-width reservation:
// the right-hand count cluster used to be ignored, so every search at 76 columns
// or narrower fell back to the four-line header and lost a body row.
func TestSearchHeaderFitsRightClusterOnOneLine(t *testing.T) {
	for _, width := range []int{100, 90, 76, 60, 50} {
		m := New(uiPkgs(), false, false, true, Dependencies{})
		m = update(t, m, tea.WindowSizeMsg{Width: width, Height: 15})
		m = update(t, m, runeKey("/"))
		if got := len(m.headerLines()); got != 3 {
			t.Fatalf("width %d search header lines = %d want 3:\n%s", width, got, m.header())
		}
	}
	// With multiple state indicators the four-line fallback remains correct.
	m := New(uiPkgs(), false, true, true, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 12})
	m.refreshing = true
	m = update(t, m, runeKey("/"))
	if got := len(m.headerLines()); got != 4 {
		t.Fatalf("minimum-width search header lines = %d want 4:\n%s", got, m.header())
	}
}

func TestDetailInstallCommandIsNeverTruncated(t *testing.T) {
	s := makeStyles(true)
	p := domain.Package{Name: "font-jetbrains-mono-nerd-font", Kind: domain.KindFont, InstallTarget: "font-jetbrains-mono-nerd-font"}
	want := platformDisplay(p)
	// 38 content cells is the narrowest wide-layout detail pane; its install
	// field is 27 cells, narrower than the 29-cell cask token.
	for _, width := range []int{38, 41, 50} {
		lines := detailLines(p, width, 100, time.Now(), true, DatesReady, s)
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "…") {
			t.Fatalf("width %d install command truncated:\n%s", width, joined)
		}
		var install []string
		started := false
		for _, line := range lines {
			switch {
			case strings.HasPrefix(line, installLabel):
				started = true
				install = append(install, strings.TrimSpace(line[len(installLabel):]))
			case started && strings.HasPrefix(line, strings.Repeat(" ", len(installLabel))):
				install = append(install, strings.TrimSpace(line[len(installLabel):]))
			default:
				started = false
			}
		}
		if got := reconstructValue(t, install, want); got != want {
			t.Fatalf("width %d install command rebuilt as %q want %q", width, got, want)
		}
		for _, line := range lines {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d detail line %d cells: %q", width, w, line)
			}
		}
	}
}

// reconstructValue rebuilds a value the never-truncating wrap split across
// lines: each rendered line must continue the original text, separated by a
// space only where the original held one. A dropped word separator or a
// truncated tail therefore changes the rebuilt string.
func reconstructValue(t *testing.T, lines []string, original string) string {
	t.Helper()
	rest := original
	var b strings.Builder
	for _, line := range lines {
		switch {
		case strings.HasPrefix(rest, line):
			b.WriteString(line)
			rest = rest[len(line):]
		case strings.HasPrefix(rest, " "+line):
			b.WriteString(" " + line)
			rest = rest[len(line)+1:]
		default:
			t.Fatalf("wrapped line %q cannot continue %q", line, rest)
		}
	}
	if rest != "" {
		t.Fatalf("wrapped lines left %q unconsumed", rest)
	}
	return b.String()
}

// TestDetailElisionSurvivesAtShortHeights pins the detail pane's elision order:
// the blank separators go first, then description lines, then the informational
// homepage, and the actionable install command is dropped last.
func TestDetailElisionSurvivesAtShortHeights(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	added := now.Add(-10 * 24 * time.Hour)
	s := makeStyles(true)
	p := domain.Package{
		Name:          "font-maple",
		Kind:          domain.KindFont,
		Description:   "Rounded monospace programming font for editing code",
		Homepage:      "https://example.test/homepage",
		InstallTarget: "font-maple",
		AddedAt:       &added,
	}
	heading := "font-maple  [font]"
	ageLine := "2026-07-27 · 10 days ago"
	desc := []string{"Rounded monospace programming font for editing", "code"}
	home := "homepage   https://example.test/homepage"
	install := "install    brew install --cask font-maple"
	for _, tc := range []struct {
		height int
		want   []string
	}{
		{1, []string{heading, ageLine}},
		{2, []string{heading, ageLine}},
		{3, []string{heading, ageLine, install}},
		{4, []string{heading, ageLine, home, install}},
		{5, []string{heading, ageLine, desc[0], home, install}},
		{6, []string{heading, ageLine, desc[0], desc[1], home, install}},
		{7, []string{heading, ageLine, "", desc[0], desc[1], home, install}},
		{8, []string{heading, ageLine, "", desc[0], desc[1], "", home, install}},
	} {
		got := detailLines(p, 48, tc.height, now, true, DatesReady, s)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("height %d = %q want %q", tc.height, got, tc.want)
		}
		for _, line := range got {
			if strings.HasPrefix(line, homepageLabel) && strings.TrimSpace(strings.TrimPrefix(line, homepageLabel)) == "" {
				t.Fatalf("height %d emitted a homepage label without a value: %q", tc.height, line)
			}
			if strings.HasPrefix(line, installLabel) && strings.TrimSpace(strings.TrimPrefix(line, installLabel)) == "" {
				t.Fatalf("height %d emitted an install label without a value: %q", tc.height, line)
			}
		}
	}
}

func TestWideFooterIndentComesFromTheBudget(t *testing.T) {
	m := New(uiPkgs(), false, false, true, Dependencies{Install: func(domain.Package) tea.Cmd { return nil }})
	m = update(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	m.setStatus(strings.Repeat("s", 120), statusSuccess)
	line := m.footerLines()[0]
	if got := lipgloss.Width(line); got != 90 {
		t.Fatalf("wide footer width = %d want 90: %q", got, line)
	}
	if !strings.HasPrefix(line, "  ") {
		t.Fatalf("wide footer lost its indent: %q", line)
	}
}

func TestPanelViewKeepsBordersOnShortTerminals(t *testing.T) {
	m := New(nil, false, false, true, Dependencies{})
	m = update(t, m, tea.WindowSizeMsg{Width: 50, Height: 4})
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 4 {
		t.Fatalf("height 4 view has %d lines:\n%s", len(lines), m.View())
	}
	if !strings.HasPrefix(lines[0], "╭") || !strings.HasPrefix(lines[3], "╰") {
		t.Fatalf("short panel lost a border:\n%s", m.View())
	}
}
