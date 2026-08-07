package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/milo/brewnicle/internal/domain"
)

type styles struct {
	title, accent, selected    lipgloss.Style
	formula, cask, font        lipgloss.Style
	success, warning, danger   lipgloss.Style
	muted, link, installPrompt lipgloss.Style
}

func makeStyles(noColor bool) styles {
	s := styles{
		title:         lipgloss.NewStyle().Bold(true),
		accent:        lipgloss.NewStyle().Bold(true).Underline(true),
		selected:      lipgloss.NewStyle().Reverse(true),
		warning:       lipgloss.NewStyle().Bold(true),
		danger:        lipgloss.NewStyle().Bold(true),
		installPrompt: lipgloss.NewStyle().Bold(true),
	}
	if noColor {
		return s
	}

	return styles{
		title:         s.title.Foreground(lipgloss.Color("5")),
		accent:        s.accent.Foreground(lipgloss.Color("6")),
		selected:      s.selected.Foreground(lipgloss.Color("6")),
		formula:       lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		cask:          lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		font:          lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		success:       lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		warning:       s.warning.Foreground(lipgloss.Color("3")),
		danger:        s.danger.Foreground(lipgloss.Color("1")),
		muted:         lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		link:          lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		installPrompt: s.installPrompt.Foreground(lipgloss.Color("3")),
	}
}

func (s styles) kind(kind domain.Kind) lipgloss.Style {
	switch kind {
	case domain.KindFormula:
		return s.formula
	case domain.KindCask:
		return s.cask
	case domain.KindFont:
		return s.font
	default:
		return lipgloss.NewStyle()
	}
}

func (s styles) kindFilter(filter domain.KindFilter) lipgloss.Style {
	switch filter {
	case domain.KindFilterFormula:
		return s.formula
	case domain.KindFilterCask:
		return s.cask
	case domain.KindFilterFont:
		return s.font
	default:
		return s.accent
	}
}

func (s styles) status(level statusLevel) lipgloss.Style {
	switch level {
	case statusSuccess:
		return s.success
	case statusWarning:
		return s.warning
	case statusError:
		return s.danger
	default:
		return lipgloss.NewStyle()
	}
}
