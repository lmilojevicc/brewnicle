package platform

import (
	"os/exec"
	"testing"
)

func TestExecResult(t *testing.T) {
	cmd := exec.Command("true")
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatal("stdio must remain nil for Bubble Tea")
	}
}
