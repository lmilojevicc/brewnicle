package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/platform"
)

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(s) + "…"
}
func wrap(s string, width int) []string {
	if width < 1 {
		return nil
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{"No description available"}
	}
	lines := []string{}
	line := ""
	for _, w := range words {
		if lipgloss.Width(w) > width {
			w = truncate(w, width)
		}
		candidate := w
		if line != "" {
			candidate = line + " " + w
		}
		if lipgloss.Width(candidate) > width {
			lines = append(lines, line)
			line = w
		} else {
			line = candidate
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
func age(t *time.Time, now time.Time, availability DateAvailability) string {
	switch availability {
	case DatesIndexing:
		return "Date indexing…"
	case DatesUnavailable:
		return "Date unavailable"
	}
	if t == nil {
		return "Date unknown"
	}
	d := now.UTC().Sub(t.UTC())
	if d < 0 {
		d = 0
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%s · %d days ago", t.UTC().Format("2006-01-02"), days)
}

// compactAge returns the three-cell list-row age: Nd under 100 days, Nmo
// through nine months, Ny from ten months on, and ? when the date is not
// known. No branch may exceed three cells, so the last stretch before a year
// rounds to the year shorthand instead of overflowing as "10mo".
func compactAge(t *time.Time, now time.Time, availability DateAvailability) string {
	if availability != DatesReady || t == nil {
		return "?"
	}
	d := now.UTC().Sub(t.UTC())
	if d < 0 {
		d = 0
	}
	days := int(d.Hours() / 24)
	switch {
	case days < 100:
		return fmt.Sprintf("%dd", days)
	case days < 300:
		return fmt.Sprintf("%dmo", days/30)
	case days < 365:
		return "1y"
	default:
		// The year shorthand is capped at 99y so the field never exceeds its
		// hard three-cell width, even for an unreachable 100-year-old date.
		return fmt.Sprintf("%dy", min(days/365, 99))
	}
}

// wrapCells hard-wraps s into lines of at most width display cells. It is used
// for a single unbreakable token, such as a package name, that must never be
// truncated.
func wrapCells(s string, width int) []string {
	if width < 1 {
		return nil
	}
	lines := make([]string, 0, 1)
	line := ""
	lineWidth := 0
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if line != "" && lineWidth+w > width {
			lines = append(lines, line)
			line = ""
			lineWidth = 0
		}
		line += string(r)
		lineWidth += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

// wrapNoTruncate word-wraps s to width without ever dropping characters: a
// single token wider than the field is split across lines instead of being
// truncated. It is used for values the user must be able to copy exactly, such
// as an install command.
func wrapNoTruncate(s string, width int) []string {
	if width < 1 {
		return nil
	}
	lines := []string{}
	line := ""
	for _, word := range strings.Fields(s) {
		for _, chunk := range wrapCells(word, width) {
			switch {
			case line == "":
				line = chunk
			case lipgloss.Width(line)+1+lipgloss.Width(chunk) <= width:
				line += " " + chunk
			default:
				lines = append(lines, line)
				line = chunk
			}
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

// fixed labels for the detail pane's value block. Each is exactly eleven
// cells so the value field is paneContent minus eleven.
const (
	homepageLabel = "homepage   "
	installLabel  = "install    "
)

// labelledLines lays out a fixed-cell label followed by value lines that wrap
// to the remaining width, indenting every continuation line by the label width
// so the value never collides with the label column. wrapValue chooses whether
// an over-long single token is truncated (wrap) or split (wrapNoTruncate).
func labelledLines(label string, labelStyle, valueStyle lipgloss.Style, value string, width int, wrapValue func(string, int) []string) []string {
	labelWidth := lipgloss.Width(label)
	if width <= labelWidth {
		wrapped := wrapValue(value, width)
		lines := make([]string, len(wrapped))
		for i, line := range wrapped {
			lines[i] = valueStyle.Render(line)
		}
		return lines
	}
	wrapped := wrapValue(value, width-labelWidth)
	indent := strings.Repeat(" ", labelWidth)
	lines := make([]string, len(wrapped))
	for i, line := range wrapped {
		if i == 0 {
			lines[i] = labelStyle.Render(label) + valueStyle.Render(line)
		} else {
			lines[i] = valueStyle.Render(indent + line)
		}
	}
	return lines
}

// detailHeading renders the detail pane's leading name line. Package names are
// never truncated: the "  [kind]" suffix is kept only when it fits, and a name
// wider than the pane is wrapped onto continuation lines instead.
func detailHeading(p domain.Package, width int, s styles) []string {
	kind := string(p.Kind)
	suffix := "  [" + kind + "]"
	if lipgloss.Width(p.Name)+lipgloss.Width(suffix) <= width {
		return []string{s.title.Render(p.Name) + "  [" + s.kind(p.Kind).Render(kind) + "]"}
	}
	wrapped := wrapCells(p.Name, width)
	lines := make([]string, len(wrapped))
	for i, line := range wrapped {
		lines[i] = s.title.Render(line)
	}
	return lines
}

// detailLines builds the right-hand detail content, fitting it to the
// available content height. Elision drops the blank separators first, then
// description lines, then the informational homepage, and the actionable exact
// install command only as an absolute last resort.
func detailLines(p domain.Package, width, height int, now time.Time, installAvailable bool, dates DateAvailability, s styles) []string {
	home := p.Homepage
	if home == "" {
		home = "Unavailable"
	}
	install := platform.InstallDisplay(p)
	if !installAvailable {
		install = "Unavailable (Homebrew not found)"
	}

	heading := detailHeading(p, width, s)
	ageLine := s.muted.Render(truncate(age(p.AddedAt, now, dates), width))
	desc := wrap(p.Description, width)
	homeLines := labelledLines(homepageLabel, s.link, s.link, home, width, wrap)
	installLines := labelledLines(installLabel, s.installPrompt, lipgloss.NewStyle(), install, width, wrapNoTruncate)

	blankAfterAge, blankBeforeLinks := true, true
	for {
		lines := assembleDetail(heading, ageLine, blankAfterAge, desc, blankBeforeLinks, homeLines, installLines)
		if height < 1 || len(lines) <= height {
			return lines
		}
		switch {
		case blankBeforeLinks:
			blankBeforeLinks = false
		case blankAfterAge:
			blankAfterAge = false
		case len(desc) > 0:
			desc = desc[:len(desc)-1]
		case len(homeLines) > 0:
			homeLines = nil
		case len(installLines) > 0:
			installLines = nil
		default:
			return lines
		}
	}
}

func assembleDetail(heading []string, ageLine string, blankAfterAge bool, desc []string, blankBeforeLinks bool, homeLines, installLines []string) []string {
	lines := make([]string, 0, len(heading)+1+len(desc)+len(homeLines)+len(installLines)+2)
	lines = append(lines, heading...)
	lines = append(lines, ageLine)
	if blankAfterAge {
		lines = append(lines, "")
	}
	lines = append(lines, desc...)
	if blankBeforeLinks {
		lines = append(lines, "")
	}
	lines = append(lines, homeLines...)
	lines = append(lines, installLines...)
	return lines
}
func fitLines(lines []string, width, height int) string {
	if height < 0 {
		height = 0
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = truncate(lines[i], width)
	}
	return strings.Join(lines, "\n")
}
