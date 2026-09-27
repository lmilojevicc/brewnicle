package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/lmilojevicc/brewnicle/internal/domain"
	"github.com/lmilojevicc/brewnicle/internal/refresh"
)

const (
	WideMinWidth        = 90
	WideMinHeight       = 16
	NarrowMinWidth      = 50
	NarrowMinHeight     = 12
	emptyResultsMessage = "No matches: / search · f type · 5 all"

	// Wide and narrow layouts spend identical chrome: a three-line header box
	// (top border, one content line, bottom border), a body with a top and a
	// bottom border, and a one-line footer, so both share a row capacity of
	// height minus six. The two body border lines are called out here.
	bodyBorderLines = 2

	// A list row is marker(2) + name + two-cell gaps + kind(7) + age(3) +
	// description. The name column sizes itself to the widest name in the
	// current filtered set and is never truncated to a narrower column, so the
	// description absorbs the cost and is dropped before any name is cut.
	rowMarkerCells = 2
	// The name column never drops below this many cells.
	nameMinCells = 12
	// Cell gaps between the name, kind badge, age, and description fields.
	rowGapCells = 2
	// Fixed tail widths: a seven-cell kind badge and a three-cell age.
	kindCells = 7
	ageCells  = 3
	// The description column needs at least this many cells or it is dropped.
	descriptionMinCells = 8
	// Budget thresholds, in cells remaining after the marker and name column,
	// for each progressively shorter row tail.
	kindMinBudget        = rowGapCells + kindCells
	kindAgeMinBudget     = kindMinBudget + rowGapCells + ageCells
	descriptionMinBudget = kindAgeMinBudget + rowGapCells + descriptionMinCells
	// Cells consumed by the kind, age, and their gaps ahead of the description.
	descriptionTailCells = kindAgeMinBudget + rowGapCells
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

// searchInputWidth reserves the boxed-header space before the search field so
// the brand, the type label, the Search: prompt, the one-cell gap, and the
// right-hand count/indicator cluster all fit on a single header line. The
// cluster width is read from the current model state because its indicators
// are never truncated, so the field shrinks as the cluster grows.
func (m Model) searchInputWidth() int {
	right := lipgloss.Width(m.headerRight(makeStyles(m.noColor), headerRightMaxWidth(m.width)))
	// The constant three is the focused input's cursor cell plus the minimum
	// gap before the right-hand cluster.
	available := m.width - 2 - lipgloss.Width("brewnicle") - 3 - lipgloss.Width("type:all") - 3 - lipgloss.Width("Search: ") - 3 - right
	if available < 8 {
		return 8
	}
	if available > 40 {
		return 40
	}
	return available
}

// headerRightMaxWidth caps the right-hand header cluster so its indicators
// never crowd the brand out of the box entirely.
func headerRightMaxWidth(width int) int {
	return max(0, width-2-lipgloss.Width("brewnicle")-1)
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if !isUsableLayout(m.width, m.height) {
		return m.panelView("", []string{"Terminal too small", "Need at least 50×12", "q quit"})
	}
	if m.state == StateBootstrap {
		return m.bootstrapView()
	}
	if m.state == StateFatal {
		return m.fatalView()
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

// widePaneWidths splits the space between the list and detail panels with a
// one-column gutter, keeping at least 32 detail content columns.
func widePaneWidths(width int) (int, int) {
	left := int(math.Round(float64(width-1) * 0.562))
	left = max(left, 40)
	left = min(left, width-1-34)
	return left, width - 1 - left
}

func (m Model) headerLines() []string {
	s := makeStyles(m.noColor)
	inner := max(0, m.width-2)
	content := m.headerContent(s, inner)
	lines := make([]string, 0, len(content)+2)
	lines = append(lines, panelBorderTop(s, "", m.width))
	for _, line := range content {
		lines = append(lines, panelContentLine(s, line, m.width))
	}
	lines = append(lines, panelBorderBottom(s, m.width))
	return lines
}

func (m Model) header() string { return strings.Join(m.headerLines(), "\n") }

func (m Model) headerContent(s styles, inner int) []string {
	right := m.headerRight(s, headerRightMaxWidth(m.width))
	if m.state == StateSearch {
		input := m.input
		input.Width = m.searchInputWidth()
		left := s.title.Render("brewnicle") + "   " + m.kindFilterLabel(s) + "   " + input.View()
		if lipgloss.Width(left)+2+lipgloss.Width(right) <= inner {
			return []string{alignHeader(left, right, inner)}
		}
		// At narrow widths the focused input keeps the first content line
		// while count, stale, and refresh state keep an untruncated line.
		return []string{truncate(left, inner), truncate(right, inner)}
	}
	return []string{m.browseHeaderLine(s, inner, right)}
}

// browseHeaderLine drops whole range tabs from the right, then the type label,
// and finally the active range tab, when the header box cannot hold brand,
// tabs, type, and the right indicators. Inactive tabs are dropped before the
// active one and the type label is dropped before the active tab so the header
// never shows a range set with none active; the count and stale/refreshing
// indicators are never truncated.
func (m Model) browseHeaderLine(s styles, inner int, right string) string {
	brand := s.title.Render("brewnicle")
	typeLabel := m.kindFilterLabel(s)
	query := ""
	if m.query != "" {
		query = s.link.Render("/" + m.query)
	}
	tabs := m.rangeTabs(s)
	active := activeRangeIndex(m.rangeValue)
	rightWidth := lipgloss.Width(right)
	withType := true
	build := func() string {
		segments := []string{brand}
		if len(tabs) > 0 {
			segments = append(segments, strings.Join(tabs, "   "))
		}
		if query != "" {
			segments = append(segments, query)
		}
		if withType {
			segments = append(segments, typeLabel)
		}
		return strings.Join(segments, "   ")
	}
	for {
		left := build()
		if lipgloss.Width(left)+2+rightWidth <= inner {
			return alignHeader(left, right, inner)
		}
		if drop := lastInactiveTab(tabs, active); drop >= 0 {
			tabs = append(tabs[:drop], tabs[drop+1:]...)
			if drop < active {
				active--
			}
			continue
		}
		if withType {
			withType = false
			continue
		}
		if len(tabs) > 0 {
			tabs, active = nil, -1
			continue
		}
		break
	}
	return alignHeader(truncate(build(), max(0, inner-rightWidth-1)), right, inner)
}

// lastInactiveTab returns the index of the rightmost tab that is not the active
// range, or -1 when only the active tab remains.
func lastInactiveTab(tabs []string, active int) int {
	for i := len(tabs) - 1; i >= 0; i-- {
		if i != active {
			return i
		}
	}
	return -1
}

func alignHeader(left, right string, inner int) string {
	gap := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// activeRangeIndex reports the position of the active range in domain.Ranges,
// or -1 when it is not one of the ordered tabs.
func activeRangeIndex(r domain.Range) int {
	for i, candidate := range domain.Ranges {
		if candidate == r {
			return i
		}
	}
	return -1
}

func (m Model) kindFilterLabel(s styles) string {
	return "type:" + s.kindFilter(m.kindFilter).Render(m.kindFilter.String())
}

func (m Model) rangeTabs(s styles) []string {
	tabs := make([]string, 0, len(domain.Ranges))
	for _, r := range domain.Ranges {
		value := string(r)
		if r == m.rangeValue {
			tabs = append(tabs, s.accent.Render("["+value+"]"))
		} else {
			tabs = append(tabs, s.muted.Render(value))
		}
	}
	return tabs
}

// headerRight renders the visible/total count and the state indicators. The
// count and the STALE / DATES INDEXING / DATES UNAVAILABLE / refreshing
// indicators are never truncated: a refresh progress detail that does not fit
// maxWidth is capped, then dropped entirely.
func (m Model) headerRight(s styles, maxWidth int) string {
	parts := []string{s.muted.Render(fmt.Sprintf("%d/%d", len(m.visible), len(m.packages)))}
	if m.stale {
		parts = append(parts, s.warning.Render("STALE"))
	}
	switch m.dates {
	case DatesIndexing:
		parts = append(parts, s.warning.Render("DATES INDEXING"))
	case DatesUnavailable:
		parts = append(parts, s.danger.Render("DATES UNAVAILABLE"))
	}
	if m.refreshing {
		indicator := s.accent.Render("refreshing")
		if !isNarrowLayout(m.width, m.height) && m.progress != "" {
			prefix := s.accent.Render("refreshing: ")
			room := maxWidth - lipgloss.Width(strings.Join(parts, "  ")) - lipgloss.Width(prefix)
			if len(parts) > 0 {
				room -= rowGapCells
			}
			if room >= 6 {
				indicator = prefix + s.accent.Render(truncate(m.progress, room))
			}
		}
		parts = append(parts, indicator)
	}
	return strings.Join(parts, "  ")
}

// rows builds list rows for a pane whose content width is width and whose
// visible row count is height. The selected row is centered when possible.
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
	nameW := m.listNameWidth()
	for i := start; i < len(m.visible) && len(out) < visibleCount; i++ {
		out = append(out, m.packageRow(m.visible[i], i == selected, width, nameW, s))
	}
	if len(out) == 0 {
		out = []string{truncate(emptyResultsMessage, width)}
	}
	return out
}

// listNameWidth sizes the list name column to the widest name in the current
// filtered set, floored at nameMinCells. Names are never truncated to a
// narrower column; the description column absorbs the cost instead.
func (m Model) listNameWidth() int {
	width := nameMinCells
	for _, p := range m.visible {
		width = max(width, lipgloss.Width(p.Name))
	}
	return width
}

func (m Model) packageRow(p domain.Package, selected bool, width, nameW int, s styles) string {
	marker := "  "
	if selected {
		marker = "› "
	}
	ageText := compactAge(p.AddedAt, m.now(), m.dates)
	budget := width - (rowMarkerCells + nameW)

	// A single visible name wider than the pane is the only unavoidable
	// truncation: there is no room for the marker or any tail.
	if budget < 0 {
		line := padCell(p.Name, width)
		if selected {
			return s.selected.Render(line)
		}
		return line
	}

	name := padCell(p.Name, nameW)
	age := padCell(ageText, ageCells)
	kind := padCell(string(p.Kind), kindCells)
	kindCell := kind
	if !selected {
		kindCell = s.kind(p.Kind).Render(kind)
	}

	var body string
	switch {
	case budget >= descriptionMinBudget:
		body = marker + name + "  " + kindCell + "  " + age + "  " + padCell(p.Description, budget-descriptionTailCells)
	case budget >= kindAgeMinBudget:
		body = marker + name + "  " + kindCell + "  " + age
	case budget >= kindMinBudget:
		body = marker + name + "  " + kindCell
	default:
		body = marker + name
	}
	body = padCell(body, width)
	if selected {
		return s.selected.Render(body)
	}
	return body
}

func (m Model) wideView() string {
	s := makeStyles(m.noColor)
	header := m.headerLines()
	footer := m.footerLines()
	contentHeight := max(0, m.height-len(header)-len(footer)-bodyBorderLines)

	leftWidth, rightWidth := widePaneWidths(m.width)
	leftBox := panelLines(s, "packages", m.rows(leftWidth-2, contentHeight), contentHeight, leftWidth)

	rightTitle := "package"
	rightContent := []string{"Select a package"}
	if p, selected := m.selectedPackage(); selected {
		rightTitle = detailPanelTitle(p.Name, rightWidth-2)
		rightContent = detailLines(p, rightWidth-2, contentHeight, m.now(), m.deps.Install != nil, m.dates, s)
	}
	rightBox := panelLines(s, rightTitle, rightContent, contentHeight, rightWidth)

	body := make([]string, len(leftBox))
	for i := range leftBox {
		body[i] = leftBox[i] + " " + rightBox[i]
	}
	lines := append(append([]string(nil), header...), body...)
	lines = append(lines, footer...)
	return fitLines(lines, m.width, m.height)
}

func (m Model) narrowView() string {
	s := makeStyles(m.noColor)
	header := m.headerLines()
	footer := m.footerLines()
	contentHeight := max(0, m.height-len(header)-len(footer)-bodyBorderLines)

	title := "packages"
	content := m.rows(m.width-2, contentHeight)
	if m.state == StateNarrowDetail {
		if p, selected := m.selectedPackage(); selected {
			title = detailPanelTitle(p.Name, m.width-2)
			content = detailLines(p, m.width-2, contentHeight, m.now(), m.deps.Install != nil, m.dates, s)
		}
	}
	body := panelLines(s, title, content, contentHeight, m.width)
	lines := append(append([]string(nil), header...), body...)
	lines = append(lines, footer...)
	return fitLines(lines, m.width, m.height)
}

// footerLines always returns exactly one line. The status message, when
// present, takes the leading cells and whole key hints are dropped when the
// remainder does not fit.
func (m Model) footerLines() []string {
	s := makeStyles(m.noColor)
	width := m.width
	var hints []string
	if m.state == StateSearch {
		hints = []string{s.muted.Render("type to filter"), s.accent.Render("enter apply"), s.muted.Render("esc keep query")}
	} else if isNarrowLayout(m.width, m.height) {
		if m.state == StateNarrowDetail {
			hints = append(hints, s.accent.Render("enter back"))
		} else {
			hints = append(hints, s.accent.Render("enter details"))
		}
		hints = append(hints, s.muted.Render("↑/↓ move"), s.accent.Render("f type"), s.link.Render("/ search"), s.muted.Render("q quit"))
		if m.deps.Install != nil {
			hints = append(hints, s.installPrompt.Render("i install"))
		}
		hints = append(hints, s.success.Render("r refresh"), s.muted.Render("? help"))
	} else {
		hints = append(hints, s.muted.Render("↑/↓ move"), s.accent.Render("f type"), s.link.Render("/ search"), s.link.Render("o homepage"))
		if m.deps.Install != nil {
			hints = append(hints, s.installPrompt.Render("i install"))
		}
		hints = append(hints, s.success.Render("r refresh"), s.muted.Render("? help"), s.muted.Render("q quit"))
	}
	partText := hints
	if m.status != "" {
		partText = append([]string{s.status(m.statusLevel).Render(m.status)}, hints...)
	}
	if m.refreshing && m.dates == DatesIndexing {
		partText = append([]string{s.accent.Render(m.indexingLabel())}, partText...)
	}
	// The wide footer is indented under the body panels; that indent comes out
	// of the join budget so the last hint is never clipped.
	indent := ""
	if isWideLayout(m.width, m.height) {
		indent = "  "
	}
	line := joinHints(width-lipgloss.Width(indent), partText...)
	if line == "" {
		return []string{""}
	}
	return []string{indent + line}
}

func (m Model) footer() string { return strings.Join(m.footerLines(), "\n") }

// joinHints keeps the first part (truncated to width if needed) and then adds
// whole later parts while they fit, dropping any that would overflow.
func joinHints(width int, parts ...string) string {
	line := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if line == "" {
			line = truncate(part, width)
			continue
		}
		candidate := line + "  " + part
		if lipgloss.Width(candidate) <= width {
			line = candidate
		}
	}
	return line
}

func (m Model) bootstrapView() string {
	s := makeStyles(m.noColor)
	inner := max(0, m.width-2)
	detail := m.progress
	if detail == "" {
		detail = "Preparing package history. First run may require significant network, time, and disk."
	}
	content := []string{s.title.Render("building first index"), ""}
	if m.refreshing {
		content = append(content, s.accent.Render(m.indexingLabel()))
	}
	for _, line := range wrap(detail, inner) {
		content = append(content, s.warning.Render(line))
	}
	if m.bootstrapDiagnostic != "" {
		content = append(content, "")
		for _, line := range wrap(m.bootstrapDiagnostic, inner) {
			content = append(content, s.danger.Render(line))
		}
	}
	content = append(content, "", s.muted.Render("This uses official Homebrew API catalogs and Git history."), s.muted.Render("q quit"))
	return m.panelView("brewnicle", content)
}

func (m Model) indexingLabel() string {
	stage := "indexing"
	switch m.progressPhase {
	case refresh.PhaseLock:
		stage = "waiting for refresh lock"
	case refresh.PhaseCatalog:
		stage = "fetching catalogs"
	case refresh.PhaseCatalogReady:
		stage = "catalog ready · indexing history"
	case refresh.PhaseHistory:
		stage = "indexing history"
	case refresh.PhasePublish:
		stage = "publishing index"
	case refresh.PhaseSnapshotReady:
		stage = "using published index"
	}
	return fmt.Sprintf("%s · elapsed %s", stage, formatElapsed(m.indexingElapsed()))
}

func formatElapsed(elapsed time.Duration) string {
	seconds := int(elapsed.Round(time.Second) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if hours := seconds / 3600; hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func (m Model) fatalView() string {
	s := makeStyles(m.noColor)
	inner := max(0, m.width-2)
	content := []string{s.danger.Render("Brewnicle could not build its index"), ""}
	for _, line := range wrap(m.status, inner) {
		content = append(content, s.danger.Render(line))
	}
	content = append(content, "", s.muted.Render("r retry  q quit"))
	return m.panelView("brewnicle", content)
}

func (m Model) helpView() string {
	s := makeStyles(m.noColor)
	return m.panelView("help", []string{
		"↑/↓ or j/k  move selection",
		"1–5  time range  ·  tab cycle",
		s.accent.Render("f/F  package type forward/back"),
		s.link.Render("/  search name and description"),
		s.link.Render("o  open homepage") + "  ·  " + s.installPrompt.Render("i  install"),
		s.success.Render("r  refresh"),
		"enter  details (narrow terminals)",
		"? or esc  close help",
		"q  quit",
	})
}

func (m Model) confirmView() string {
	s := makeStyles(m.noColor)
	inner := max(0, m.width-2)
	command := []string{s.installPrompt.Render("")}
	if m.hasConfirmation {
		// The command is what the user copies, so wrap it rather than
		// truncate it.
		command = nil
		for _, line := range wrapNoTruncate(platformDisplay(m.confirmPackage), inner) {
			command = append(command, s.installPrompt.Render(line))
		}
	}
	content := confirmContent(
		s.warning.Render("Confirm installation"),
		command,
		s.muted.Render("Enter/y install  Esc/n cancel"),
		max(0, m.height-2),
	)
	return m.panelView("install", content)
}

// confirmContent keeps the exact install command and drops only the optional
// blank separators, then the control hint, when the modal cannot fit the
// terminal. The command always wins over optional prose.
func confirmContent(title string, command []string, controls string, limit int) []string {
	blankBefore, blankAfter, showControls := true, true, true
	for {
		content := []string{title}
		if blankBefore {
			content = append(content, "")
		}
		content = append(content, command...)
		if blankAfter {
			content = append(content, "")
		}
		if showControls {
			content = append(content, controls)
		}
		if limit <= 0 || len(content) <= limit {
			return content
		}
		switch {
		case blankAfter:
			blankAfter = false
		case blankBefore:
			blankBefore = false
		case showControls:
			showControls = false
		default:
			return content
		}
	}
}

// panelView renders one bordered box sized to its content and fits the result
// to the terminal, padding with blank lines. Content is clamped so the top and
// bottom borders always survive a short terminal.
func (m Model) panelView(title string, content []string) string {
	s := makeStyles(m.noColor)
	content = clampMessageContent(content, max(0, m.height-2))
	return fitLines(panelLines(s, title, content, len(content), m.width), m.width, m.height)
}

// clampMessageContent trims content to at most limit lines, keeping the first
// (the panel's identifying line) and the last (its control line) and dropping
// from the middle. When only one line fits the control line wins, because every
// simple panel ends with an explicit quit or confirm control that must stay
// reachable on a tiny terminal.
func clampMessageContent(content []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if len(content) <= limit {
		return content
	}
	if limit == 1 {
		return content[len(content)-1:]
	}
	out := make([]string, 0, limit)
	out = append(out, content[0])
	out = append(out, content[len(content)-(limit-1):]...)
	return out
}

// detailPanelTitle keeps the package name in the detail pane's border title
// only when the whole name fits; otherwise it falls back to the generic label
// so the border never shows a name truncated next to the full content heading.
func detailPanelTitle(name string, inner int) string {
	if lipgloss.Width("─ "+name+" ") <= inner {
		return name
	}
	return "package"
}

// panelLines renders a bordered box of the given width with exactly height
// content rows plus a top and bottom border.
func panelLines(s styles, title string, content []string, height, width int) []string {
	if width < 2 {
		if height < 0 {
			height = 0
		}
		if len(content) > height {
			content = content[:height]
		}
		return content
	}
	lines := make([]string, 0, height+2)
	lines = append(lines, panelBorderTop(s, title, width))
	for i := 0; i < height; i++ {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		lines = append(lines, panelContentLine(s, line, width))
	}
	lines = append(lines, panelBorderBottom(s, width))
	return lines
}

func panelBorderTop(s styles, title string, width int) string {
	inner := max(0, width-2)
	if title == "" {
		return s.border.Render("╭" + strings.Repeat("─", inner) + "╮")
	}
	prefix := truncate("─ "+title+" ", inner)
	return s.border.Render("╭" + prefix + strings.Repeat("─", max(0, inner-lipgloss.Width(prefix))) + "╮")
}

func panelBorderBottom(s styles, width int) string {
	return s.border.Render("╰" + strings.Repeat("─", max(0, width-2)) + "╯")
}

func panelContentLine(s styles, line string, width int) string {
	return s.border.Render("│") + padCell(line, max(0, width-2)) + s.border.Render("│")
}

// padCell truncates s to width terminal cells and right-pads it with spaces so
// the result is exactly width cells wide.
func padCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = truncate(s, width)
	if gap := width - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
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
