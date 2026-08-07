package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/milo/brewnicle/internal/domain"
)

const (
	WideMinWidth        = 90
	WideMinHeight       = 16
	NarrowMinWidth      = 50
	NarrowMinHeight     = 12
	emptyResultsMessage = "No matches: / search · f type · 5 all"
)

func isUsableLayout(width, height int) bool {
	return width >= NarrowMinWidth && height >= NarrowMinHeight
}

func isWideLayout(width, height int) bool {
	return width >= WideMinWidth && height >= WideMinHeight
}

func isNarrowLayout(width, height int) bool {
	return isUsableLayout(width, height) && !isWideLayout(width, height)
}

func searchInputWidth(width int) int {
	available := width - 28
	if available < 8 {
		return 8
	}
	if available > 40 {
		return 40
	}
	return available
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if !isUsableLayout(m.width, m.height) {
		return fitLines([]string{"Terminal too small", "Need at least 50×12", "q quit"}, m.width, m.height)
	}
	if m.state == StateBootstrap {
		return m.bootstrapView()
	}
	if m.state == StateFatal {
		s := makeStyles(m.noColor)
		lines := []string{s.danger.Render("Brewnicle could not build its index"), ""}
		for _, line := range wrap(m.status, m.width) {
			lines = append(lines, s.danger.Render(line))
		}
		lines = append(lines, "", s.muted.Render("r retry  q quit"))
		return fitLines(lines, m.width, m.height)
	}
	if m.state == StateHelp {
		return m.helpView()
	}
	if m.state == StateConfirm {
		return m.confirmView()
	}
	if isWideLayout(m.width, m.height) {
		return m.wideView()
	}
	return m.narrowView()
}

func (m Model) header() string { return strings.Join(m.headerLines(), "\n") }

func (m Model) headerLines() []string {
	s := makeStyles(m.noColor)
	compact := isNarrowLayout(m.width, m.height)
	rightParts := []string{s.muted.Render(fmt.Sprintf("%d/%d", len(m.visible), len(m.packages)))}
	if m.stale {
		rightParts = append(rightParts, s.warning.Render("STALE"))
	}
	if m.refreshing {
		if compact || m.progress == "" {
			rightParts = append(rightParts, s.accent.Render("refreshing"))
		} else {
			rightParts = append(rightParts, s.accent.Render("refreshing: "+m.progress))
		}
	}
	right := strings.Join(rightParts, "  ")
	kind := "type:" + s.kindFilter(m.kindFilter).Render(m.kindFilter.String())
	if m.state == StateSearch {
		primary := s.title.Render("brewnicle") + "  " + kind + "  " + m.input.View()
		combined := primary + "  " + right
		if lipgloss.Width(combined) <= m.width {
			return []string{combined}
		}
		// At narrow widths the input remains visible on the first line while
		// count, stale, and refresh state get an untruncated priority line.
		return []string{truncate(primary, m.width), truncate(right, m.width)}
	}
	tabs := make([]string, 0, len(domain.Ranges))
	for _, r := range domain.Ranges {
		value := string(r)
		if r == m.rangeValue {
			value = s.accent.Render("[" + value + "]")
		} else {
			value = s.muted.Render(value)
		}
		tabs = append(tabs, value)
	}
	query := ""
	if m.query != "" {
		query = "  " + s.link.Render("/"+m.query)
	}
	primary := s.title.Render("brewnicle") + "  " + strings.Join(tabs, " ") + query
	combined := primary + "  " + kind + "  " + right
	if lipgloss.Width(combined) <= m.width {
		return []string{combined}
	}
	return []string{truncate(primary, m.width), truncate(kind+"  "+right, m.width)}
}

func (m Model) rows(width, height int) []string {
	if height <= 0 {
		return nil
	}
	s := makeStyles(m.noColor)
	out := make([]string, 0, height)
	visibleCount := min(height, len(m.visible))
	start := 0
	selected := m.selected
	if visibleCount > 0 {
		selected = min(max(selected, 0), len(m.visible)-1)
		maxStart := max(0, len(m.visible)-visibleCount)
		start = min(max(selected-visibleCount/2, 0), maxStart)
	}
	for i := start; i < len(m.visible) && len(out) < visibleCount; i++ {
		p := m.visible[i]
		marker := "  "
		if i == selected {
			marker = "› "
		}
		nameWidth := max(6, width-12)
		prefix := fmt.Sprintf("%s%-*s  ", marker, nameWidth, truncate(p.Name, nameWidth))
		row := prefix + s.kind(p.Kind).Render(string(p.Kind))
		if i == selected {
			row = s.selected.Render(prefix + string(p.Kind))
		}
		row = truncate(row, width)
		out = append(out, row)
	}
	if len(out) == 0 {
		out = []string{emptyResultsMessage}
	}
	return out
}

func (m Model) wideView() string {
	s := makeStyles(m.noColor)
	header := m.headerLines()
	footer := m.footerLines()
	bodyHeight := m.height - len(header) - len(footer)
	leftWidth := m.width*2/5 - 1
	rightWidth := m.width - leftWidth - 3
	left := fitLines(m.rows(leftWidth, bodyHeight), leftWidth, bodyHeight)
	rightLines := []string{"Select a package"}
	if p, selected := m.selectedPackage(); selected {
		rightLines = detailLines(p, rightWidth, m.now(), m.deps.Install != nil, s)
	}
	right := fitLines(rightLines, rightWidth, bodyHeight)
	separator := strings.TrimSuffix(strings.Repeat(" │ \n", bodyHeight), "\n")
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, separator, right)
	lines := append([]string(nil), header...)
	lines = append(lines, strings.Split(body, "\n")...)
	lines = append(lines, footer...)
	return fitLines(lines, m.width, m.height)
}

func (m Model) narrowView() string {
	s := makeStyles(m.noColor)
	header := m.headerLines()
	footer := m.footerLines()
	bodyHeight := m.height - len(header) - len(footer)
	var body string
	if m.state == StateNarrowDetail {
		if p, selected := m.selectedPackage(); selected {
			body = fitLines(detailLines(p, m.width, m.now(), m.deps.Install != nil, s), m.width, bodyHeight)
		} else {
			body = fitLines(m.rows(m.width, bodyHeight), m.width, bodyHeight)
		}
	} else {
		body = fitLines(m.rows(m.width, bodyHeight), m.width, bodyHeight)
	}
	lines := append([]string(nil), header...)
	lines = append(lines, strings.Split(body, "\n")...)
	lines = append(lines, footer...)
	return fitLines(lines, m.width, m.height)
}

func (m Model) footerLines() []string {
	s := makeStyles(m.noColor)
	if m.state == StateSearch {
		hints := s.muted.Render("type to filter") + "  " + s.accent.Render("enter apply") + "  " + s.muted.Render("esc keep query")
		if m.status != "" {
			return []string{s.status(m.statusLevel).Render(truncate(m.status, m.width)), truncate(hints, m.width)}
		}
		return []string{truncate(hints, m.width)}
	}
	var hints string
	if isNarrowLayout(m.width, m.height) {
		if m.state == StateNarrowDetail {
			hints = s.accent.Render("enter back")
		} else {
			hints = s.accent.Render("enter details")
		}
		hints = appendHint(hints, s.muted.Render("↑/↓ move"), m.width)
		hints = appendHint(hints, s.accent.Render("f type"), m.width)
		hints = appendHint(hints, s.link.Render("/ search"), m.width)
		hints = appendHint(hints, s.muted.Render("q quit"), m.width)
		if m.deps.Install != nil {
			hints = appendHint(hints, s.installPrompt.Render("i install"), m.width)
		}
		hints = appendHint(hints, s.success.Render("r refresh"), m.width)
		hints = appendHint(hints, s.muted.Render("? help"), m.width)
	} else {
		hints = s.muted.Render("↑/↓ move")
		hints = appendHint(hints, s.accent.Render("f type"), m.width)
		hints = appendHint(hints, s.link.Render("/ search"), m.width)
		hints = appendHint(hints, s.link.Render("o homepage"), m.width)
		if m.deps.Install != nil {
			hints = appendHint(hints, s.installPrompt.Render("i install"), m.width)
		}
		hints = appendHint(hints, s.success.Render("r refresh"), m.width)
		hints = appendHint(hints, s.muted.Render("? help"), m.width)
		hints = appendHint(hints, s.muted.Render("q quit"), m.width)
	}
	if m.status != "" {
		return []string{s.status(m.statusLevel).Render(truncate(m.status, m.width)), hints}
	}
	return []string{hints}
}

func appendHint(current, next string, width int) string {
	candidate := current + "  " + next
	if lipgloss.Width(candidate) <= width {
		return candidate
	}
	return current
}

func (m Model) footer() string { return strings.Join(m.footerLines(), "\n") }

func (m Model) bootstrapView() string {
	s := makeStyles(m.noColor)
	detail := m.progress
	if detail == "" {
		detail = "Preparing package history. First run may require significant network, time, and disk."
	}
	lines := []string{s.title.Render("brewnicle — building first index"), "", s.warning.Render(detail)}
	if m.bootstrapDiagnostic != "" {
		lines = append(lines, "")
		for _, line := range wrap(m.bootstrapDiagnostic, m.width) {
			lines = append(lines, s.danger.Render(line))
		}
	}
	lines = append(lines, "", s.muted.Render("This uses official Homebrew API catalogs and Git history."), s.muted.Render("q quit"))
	return fitLines(lines, m.width, m.height)
}

func (m Model) helpView() string {
	s := makeStyles(m.noColor)
	return fitLines([]string{s.title.Render("Brewnicle help"), "↑/↓ or j/k  move", "1–5  time range", "tab/shift+tab  cycle range", s.accent.Render("f/F  package type forward/back"), s.link.Render("/  search"), s.link.Render("o  homepage"), s.installPrompt.Render("i  install (with confirmation)"), s.success.Render("r  refresh"), "enter  narrow details", "? or esc  close help", "q  quit"}, m.width, m.height)
}

func (m Model) confirmView() string {
	s := makeStyles(m.noColor)
	command := ""
	if p, selected := m.selectedPackage(); selected {
		command = platformDisplay(p)
	}
	return fitLines([]string{s.warning.Render("Confirm installation"), "", s.installPrompt.Render(command), "", s.muted.Render("Enter/y install  Esc/n cancel")}, m.width, m.height)
}

func platformDisplay(p domain.Package) string {
	if p.Kind == domain.KindFormula {
		return "brew install " + p.Name
	}
	return "brew install --cask " + p.Name
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
