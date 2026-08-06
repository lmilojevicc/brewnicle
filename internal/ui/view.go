package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/milo/brewnicle/internal/domain"
)

const (
	WideMinWidth    = 90
	WideMinHeight   = 16
	NarrowMinWidth  = 50
	NarrowMinHeight = 12
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
		lines := []string{"Brewnicle could not build its index", ""}
		lines = append(lines, wrap(m.status, m.width)...)
		lines = append(lines, "", "r retry  q quit")
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
	rightParts := []string{fmt.Sprintf("%d/%d", len(m.visible), len(m.packages))}
	if m.stale {
		rightParts = append(rightParts, "STALE")
	}
	if m.refreshing {
		if compact || m.progress == "" {
			rightParts = append(rightParts, "refreshing")
		} else {
			rightParts = append(rightParts, "refreshing: "+m.progress)
		}
	}
	right := strings.Join(rightParts, "  ")
	if m.state == StateSearch {
		primary := s.title.Render("brewnicle") + "  " + m.input.View()
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
		}
		tabs = append(tabs, value)
	}
	query := ""
	if m.query != "" {
		query = "  /" + m.query
	}
	return []string{truncate(s.title.Render("brewnicle")+"  "+strings.Join(tabs, " ")+query+"  "+right, m.width)}
}

func (m Model) rows(width, height int) []string {
	s := makeStyles(m.noColor)
	out := make([]string, 0, height)
	start := 0
	if m.selected >= height {
		start = m.selected - height + 1
	}
	for i := start; i < len(m.visible) && len(out) < height; i++ {
		p := m.visible[i]
		marker := "  "
		if i == m.selected {
			marker = "› "
		}
		nameWidth := max(6, width-12)
		row := fmt.Sprintf("%s%-*s  %s", marker, nameWidth, truncate(p.Name, nameWidth), p.Kind)
		row = truncate(row, width)
		if i == m.selected {
			row = s.selected.Render(row)
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		out = []string{"No packages match. Try / search or all."}
	}
	return out
}

func (m Model) wideView() string {
	header := m.headerLines()
	footer := m.footerLines()
	bodyHeight := m.height - len(header) - len(footer)
	leftWidth := m.width*2/5 - 1
	rightWidth := m.width - leftWidth - 3
	left := fitLines(m.rows(leftWidth, bodyHeight), leftWidth, bodyHeight)
	rightLines := []string{"Select a package"}
	if p, selected := m.selectedPackage(); selected {
		rightLines = detailLines(p, rightWidth, m.now(), m.deps.Install != nil)
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
	header := m.headerLines()
	footer := m.footerLines()
	bodyHeight := m.height - len(header) - len(footer)
	var body string
	if m.state == StateNarrowDetail {
		if p, selected := m.selectedPackage(); selected {
			body = fitLines(detailLines(p, m.width, m.now(), m.deps.Install != nil), m.width, bodyHeight)
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
	if m.state == StateSearch {
		hints := "type to filter  enter apply  esc keep query"
		if m.status != "" {
			return []string{truncate(m.status, m.width), truncate(hints, m.width)}
		}
		return []string{truncate(hints, m.width)}
	}
	var hints string
	if isNarrowLayout(m.width, m.height) {
		if m.state == StateNarrowDetail {
			hints = "enter back"
		} else {
			hints = "enter details"
		}
		hints = appendHint(hints, "↑/↓ move", m.width)
		hints = appendHint(hints, "/ search", m.width)
		hints = appendHint(hints, "q quit", m.width)
		if m.deps.Install != nil {
			hints = appendHint(hints, "i install", m.width)
		}
		hints = appendHint(hints, "r refresh", m.width)
		hints = appendHint(hints, "? help", m.width)
	} else {
		hints = "↑/↓ move  / search  o homepage"
		if m.deps.Install != nil {
			hints += "  i install"
		}
		hints += "  r refresh  ? help  q quit"
	}
	if m.status != "" {
		return []string{truncate(m.status, m.width), hints}
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
	detail := m.progress
	if detail == "" {
		detail = "Preparing package history. First run may require significant network, time, and disk."
	}
	lines := []string{"brewnicle — building first index", "", detail}
	if m.bootstrapDiagnostic != "" {
		lines = append(lines, "")
		lines = append(lines, wrap(m.bootstrapDiagnostic, m.width)...)
	}
	lines = append(lines, "", "This uses official Homebrew API catalogs and Git history.", "q quit")
	return fitLines(lines, m.width, m.height)
}

func (m Model) helpView() string {
	return fitLines([]string{"Brewnicle help", "", "↑/↓ or j/k  move", "1–5  time range", "tab/shift+tab  cycle range", "/  search", "o  homepage", "i  install (with confirmation)", "r  refresh", "enter  narrow details", "? or esc  close help", "q  quit"}, m.width, m.height)
}

func (m Model) confirmView() string {
	command := ""
	if p, selected := m.selectedPackage(); selected {
		command = platformDisplay(p)
	}
	return fitLines([]string{"Confirm installation", "", command, "", "Enter/y install  Esc/n cancel"}, m.width, m.height)
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
