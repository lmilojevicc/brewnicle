package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandErrorNamesUsefulOperationAndPredicateStatus(t *testing.T) {
	err := newCommandError([]string{"--git-dir", "/tmp/x", "merge-base", "--is-ancestor", "a", "b"}, []byte("no"), errors.New("exit status 1"))
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Operation != "merge-base" || !strings.Contains(err.Error(), "git merge-base") {
		t.Fatal(err)
	}
	commandErr.Status = 1
	if status, ok := commandStatus(commandErr); !ok || status != 1 {
		t.Fatal(status, ok)
	}
	logErr := newCommandError([]string{"--git-dir", "/tmp/x", "log", "deadbeef"}, nil, errors.New("exit status 2"))
	if !strings.Contains(logErr.Error(), "git log") {
		t.Fatal(logErr)
	}
}

func TestExecGitCancellationAndDeadlineRemainContextErrors(t *testing.T) {
	script := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (ExecGit{Binary: script}).Run(ctx, "", "fetch"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := (ExecGit{Binary: script}).Run(ctx, "", "fetch"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestExecGitLaunchFailureAndBoundedOutput(t *testing.T) {
	if _, err := (ExecGit{Binary: filepath.Join(t.TempDir(), "missing")}).Run(context.Background(), "", "fetch"); err == nil || !strings.Contains(err.Error(), "git fetch") || !commandLaunchFailed(err) {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "noisy")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nhead -c 20000 /dev/zero | tr '\\000' x >&2\nexit 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := (ExecGit{Binary: script}).Run(context.Background(), "", "update-ref")
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Operation != "update-ref" || len(commandErr.Output) > 8192 {
		t.Fatal(err, len(commandErr.Output))
	}
}
