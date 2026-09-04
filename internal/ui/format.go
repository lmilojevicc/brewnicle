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
func detailLines(p domain.Package, width int, now time.Time, installAvailable bool, dates DateAvailability, s styles) []string {
	home := p.Homepage
	if home == "" {
		home = "Unavailable"
	}
	install := platform.InstallDisplay(p)
	if !installAvailable {
		install = "Unavailable (Homebrew not found)"
	}

	kind := string(p.Kind)
	suffix := "  [" + kind + "]"
	heading := s.title.Render(truncate(p.Name, width))
	if lipgloss.Width(suffix) < width {
		nameWidth := width - lipgloss.Width(suffix)
		heading = s.title.Render(truncate(p.Name, nameWidth)) + "  [" + s.kind(p.Kind).Render(kind) + "]"
	}
	lines := []string{heading, s.muted.Render(truncate(age(p.AddedAt, now, dates), width)), ""}
	lines = append(lines, wrap(p.Description, width)...)
	lines = append(lines, "")
	lines = append(lines, s.link.Render(truncate("Homepage: "+home, width)))
	installLabel := s.installPrompt.Render("Install: ")
	if lipgloss.Width(installLabel) >= width {
		lines = append(lines, s.installPrompt.Render(truncate("Install: "+install, width)))
	} else {
		lines = append(lines, installLabel+truncate(install, width-lipgloss.Width(installLabel)))
	}
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
