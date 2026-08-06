package ui

import "github.com/charmbracelet/lipgloss"

type styles struct{ title, accent, muted, selected, warn lipgloss.Style }

func makeStyles(noColor bool) styles {
	s := styles{title: lipgloss.NewStyle().Bold(true), selected: lipgloss.NewStyle().Reverse(true), warn: lipgloss.NewStyle().Bold(true)}
	if !noColor {
		s.accent = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
		s.muted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.warn = s.warn.Foreground(lipgloss.Color("3"))
	}
	return s
}
