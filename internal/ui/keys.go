package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/milo/brewnicle/internal/domain"
)

func domainRange(i int) domain.Range {
	if i < 0 || i >= len(domain.Ranges) {
		return domain.DefaultRange
	}
	return domain.Ranges[i]
}
func cycle(r domain.Range, d int) domain.Range { return domain.CycleRange(r, d) }
func textinputBlink() tea.Cmd                  { return textinput.Blink }
