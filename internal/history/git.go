package history

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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
	operations := map[string]bool{"cat-file": true, "clone": true, "config": true, "fetch": true, "for-each-ref": true, "fsck": true, "log": true, "ls-remote": true, "merge-base": true, "rev-parse": true, "symbolic-ref": true, "update-ref": true}
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

func isLocalResourceError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"cannot allocate memory", "input/output error", "i/o error", "no space left", "operation not permitted",
		"out of memory", "permission denied", "read-only file system", "resource temporarily unavailable", "too many open files",
	} {
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
	runDir := dir
	if runDir == "" && !hasExplicitRepository(args) {
		var err error
		runDir, err = os.MkdirTemp("", "brewnicle-git-")
		if err != nil {
			return nil, fmt.Errorf("create trusted git working directory: %w", err)
		}
		defer os.RemoveAll(runDir)
		if err = os.Chmod(runDir, 0700); err != nil {
			return nil, fmt.Errorf("secure trusted git working directory: %w", err)
		}
	}
	runArgs := hardenGitArgs(args)
	cmd := exec.CommandContext(ctx, bin, runArgs...)
	cmd.Dir = runDir
	cmd.Env = hardenedGitEnvironment(os.Environ())
	if runDir != "" {
		cmd.Env = append(cmd.Env, "GIT_CEILING_DIRECTORIES="+runDir)
	}
	if containsHTTPSRemote(args) {
		cmd.Env = append(cmd.Env, "GIT_ALLOW_PROTOCOL=https")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, n: 8192}
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8192}
	if err := cmd.Run(); err != nil {
		combined := append(append([]byte(nil), stdout.Bytes()...), stderr.Bytes()...)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return combined, ctxErr
		}
		return combined, newCommandError(runArgs, combined, err)
	}
	return stdout.Bytes(), nil
}

func hardenGitArgs(args []string) []string {
	hardened := []string{"--no-replace-objects"}
	op := gitOperation(args)
	if op == "clone" || op == "fetch" || op == "update-ref" {
		hardened = append(hardened, "-c", "core.hooksPath=/dev/null")
	}
	if containsHTTPSRemote(args) {
		hardened = append(hardened,
			"-c", "protocol.allow=never",
			"-c", "protocol.https.allow=always",
			"-c", "credential.helper=",
		)
	}
	return append(hardened, args...)
}

func hasExplicitRepository(args []string) bool {
	for _, arg := range args {
		if arg == "--git-dir" || strings.HasPrefix(arg, "--git-dir=") {
			return true
		}
	}
	return false
}

func containsHTTPSRemote(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "https://") || strings.Contains(lower, "=https://") {
			return true
		}
	}
	return false
}

// hardenedGitEnvironment is shared by buffered and streaming Git commands.
// Git-specific variables are default-deny because new Git releases may add
// controls that change repository selection, object lookup, or transport.
// Proxy and CA overrides are also removed: Brewnicle only contacts compiled
// HTTPS remotes in production and must use the host's normal TLS trust store.
// PATH, locale, HOME, and unrelated process settings remain available.
func hardenedGitEnvironment(environ []string) []string {
	blockedTransport := map[string]bool{
		"ALL_PROXY":      true,
		"CURL_CA_BUNDLE": true,
		"HTTPS_PROXY":    true,
		"HTTP_PROXY":     true,
		"NO_PROXY":       true,
		"SSL_CERT_DIR":   true,
		"SSL_CERT_FILE":  true,
		"SSH_ASKPASS":    true,
	}
	out := make([]string, 0, len(environ)+4)
	for _, value := range environ {
		key, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "GIT_") || blockedTransport[key] {
			continue
		}
		out = append(out, value)
	}
	return append(out, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0")
}
