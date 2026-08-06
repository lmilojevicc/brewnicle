package ui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/platform"
	"strings"
	"time"
	"unicode/utf8"
)

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	out := ""
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if lipgloss.Width(out+string(r)+"…") > width {
			break
		}
		out += string(r)
		s = s[n:]
	}
	return out + "…"
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
func age(t *time.Time, now time.Time) string {
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
func detailLines(p domain.Package, width int, now time.Time, installAvailable bool) []string {
	home := p.Homepage
	if home == "" {
		home = "Unavailable"
	}
	install := platform.InstallDisplay(p)
	if !installAvailable {
		install = "Unavailable (Homebrew not found)"
	}
	lines := []string{p.Name + "  [" + string(p.Kind) + "]", age(p.AddedAt, now), ""}
	lines = append(lines, wrap(p.Description, width)...)
	lines = append(lines, "", "Homepage: "+home, "Install: "+install)
	for i := range lines {
		lines[i] = truncate(lines[i], width)
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
