package platform

import (
	"context"
	"os/exec"
)

type LookupFunc func(string) (string, error)
type Runner interface {
	Run(context.Context, *exec.Cmd) error
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	}
	return cmd.Run()
}
func lookup(fn LookupFunc, name string) (string, error) {
	if fn == nil {
		fn = exec.LookPath
	}
	return fn(name)
}
