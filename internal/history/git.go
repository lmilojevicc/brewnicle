package history

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type CommandError struct {
	Args      []string
	Operation string
	Output    string
	Status    int
	Err       error
}

func (e *CommandError) Error() string {
	op := e.Operation
	if op == "" {
		op = gitOperation(e.Args)
	}
	if op == "" {
		op = "command"
	}
	return fmt.Sprintf("git %s: %v: %s", op, e.Err, e.Output)
}
func (e *CommandError) Unwrap() error { return e.Err }

func newCommandError(args []string, output []byte, err error) error {
	status := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		status = exit.ExitCode()
	}
	if len(output) > 8192 {
		output = output[:8192]
	}
	return &CommandError{Args: append([]string(nil), args...), Operation: gitOperation(args), Output: string(output), Status: status, Err: err}
}

func gitOperation(args []string) string {
	operations := map[string]bool{"cat-file": true, "clone": true, "fetch": true, "for-each-ref": true, "log": true, "ls-remote": true, "merge-base": true, "rev-parse": true, "update-ref": true}
	for _, arg := range args {
		if operations[arg] {
			return arg
		}
	}
	return ""
}

func commandStatus(err error) (int, bool) {
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		return commandErr.Status, true
	}
	return 0, false
}

func commandLaunchFailed(err error) bool {
	status, ok := commandStatus(err)
	return ok && status == -1
}

func isNetworkOrAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{"authentication failed", "could not resolve host", "couldn't connect", "connection timed out", "connection refused", "permission denied (publickey)", "repository not found", "http 401", "http 403"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

type GitRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type ExecGit struct{ Binary string }

func (g ExecGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	bin := g.Binary
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, n: 8192}
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8192}
	if err := cmd.Run(); err != nil {
		combined := append(append([]byte(nil), stdout.Bytes()...), stderr.Bytes()...)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return combined, ctxErr
		}
		return combined, newCommandError(args, combined, err)
	}
	return stdout.Bytes(), nil
}
