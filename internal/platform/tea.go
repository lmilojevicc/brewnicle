package platform

import (
	tea "github.com/charmbracelet/bubbletea"
	"os/exec"
)

type ExecResult struct{ Err error }
type TeaExecFunc func(*exec.Cmd, tea.ExecCallback) tea.Cmd

func TeaExec(cmd *exec.Cmd) tea.Cmd {
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return ExecResult{Err: err} })
}
