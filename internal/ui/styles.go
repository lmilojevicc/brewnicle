package ui

import "github.com/charmbracelet/lipgloss"

type styles struct{ title, accent, selected lipgloss.Style }

func makeStyles(noColor bool) styles {
	_ = noColor // retained for constructor compatibility; all styles inherit terminal colors.
	return styles{
		title:    lipgloss.NewStyle().Bold(true),
		accent:   lipgloss.NewStyle().Bold(true),
		selected: lipgloss.NewStyle().Reverse(true),
	}
}
