package history

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

func TestGitMutationHardeningDisablesHooksAndRestrictsHTTPS(t *testing.T) {
	args := hardenGitArgs([]string{"--git-dir", "/tmp/repo", "fetch", CoreRemote, "+refs/heads/main:refs/brewnicle/fetched"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"core.hooksPath=/dev/null", "protocol.allow=never", "protocol.https.allow=always", "credential.helper=", CoreRemote} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, args)
		}
	}
	lsRemote := strings.Join(hardenGitArgs([]string{"ls-remote", CoreRemote, "HEAD"}), " ")
	for _, want := range []string{"protocol.allow=never", "protocol.https.allow=always", "credential.helper="} {
		if !strings.Contains(lsRemote, want) {
			t.Fatalf("HTTPS ls-remote missing %q: %s", want, lsRemote)
		}
	}
	pinnedFetch := strings.Join(hardenGitArgs([]string{"--git-dir", "/tmp/repo", "-c", "remote.origin.url=" + CoreRemote, "fetch", "origin"}), " ")
	for _, want := range []string{"core.hooksPath=/dev/null", "protocol.allow=never", "protocol.https.allow=always", "credential.helper="} {
		if !strings.Contains(pinnedFetch, want) {
			t.Fatalf("pinned HTTPS fetch missing %q: %s", want, pinnedFetch)
		}
	}
	local := strings.Join(hardenGitArgs([]string{"clone", "/tmp/local", "/tmp/dst"}), " ")
	if !strings.Contains(local, "core.hooksPath=/dev/null") || strings.Contains(local, "protocol.allow=never") {
		t.Fatalf("local fixture transport was unexpectedly disabled: %s", local)
	}
	env := hardenedGitEnvironment([]string{"PATH=/bin", "GIT_ALLOW_PROTOCOL=ext", "GIT_CONFIG_COUNT=1", "GIT_ALTERNATE_OBJECT_DIRECTORIES=/tmp/objects", "GIT_SSH_COMMAND=evil"})
	envText := strings.Join(env, "\n")
	if strings.Contains(envText, "GIT_ALLOW_PROTOCOL=ext") || strings.Contains(envText, "GIT_CONFIG_COUNT=1") || strings.Contains(envText, "GIT_ALTERNATE_OBJECT_DIRECTORIES=/tmp/objects") || strings.Contains(envText, "GIT_SSH_COMMAND=evil") || !strings.Contains(envText, "GIT_CONFIG_NOSYSTEM=1") || !strings.Contains(envText, "GIT_CONFIG_GLOBAL="+os.DevNull) {
		t.Fatalf("unsafe environment survived: %v", env)
	}
}

func TestHardenedGitEnvironmentDefaultsDenyGitAndTransportOverrides(t *testing.T) {
	hostile := []string{
		"GIT_SSL_NO_VERIFY=true",
		"git_ssl_cainfo=/tmp/attacker-ca",
		"GIT_SHALLOW_FILE=/tmp/shallow",
		"GIT_NAMESPACE=attacker",
		"GIT_CONFIG_COUNT=1",
		"GIT_OBJECT_DIRECTORY=/tmp/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/tmp/alternates",
		"GIT_COMMON_DIR=/tmp/common",
		"GIT_INDEX_FILE=/tmp/index",
		"GIT_WORK_TREE=/tmp/tree",
		"GIT_REPLACE_REF_BASE=refs/replace/attacker",
		"GIT_ALLOW_PROTOCOL=ext:file",
		"HTTPS_PROXY=http://attacker.test:8080",
		"http_proxy=http://attacker.test:8081",
		"ALL_PROXY=socks5://attacker.test",
		"NO_PROXY=github.com",
		"CURL_CA_BUNDLE=/tmp/attacker-ca",
		"SSL_CERT_FILE=/tmp/attacker-cert",
		"SSL_CERT_DIR=/tmp/attacker-certs",
		"SSH_ASKPASS=/tmp/askpass",
	}
	env := hardenedGitEnvironment(append([]string{"PATH=/bin", "LANG=en_US.UTF-8", "HOME=/home/test"}, hostile...))
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, value := range hostile {
		if strings.Contains(strings.ToUpper(joined), "\n"+strings.ToUpper(value)+"\n") {
			t.Errorf("hostile environment survived: %s", value)
		}
	}
	for _, retained := range []string{"PATH=/bin", "LANG=en_US.UTF-8", "HOME=/home/test"} {
		if !strings.Contains(joined, "\n"+retained+"\n") {
			t.Errorf("required environment was removed: %s", retained)
		}
	}
	gotGit := map[string]string{}
	for _, value := range env {
		key, raw, ok := strings.Cut(value, "=")
		if ok && strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			gotGit[strings.ToUpper(key)] = raw
		}
	}
	wantGit := map[string]string{
		"GIT_CONFIG_GLOBAL":      os.DevNull,
		"GIT_CONFIG_NOSYSTEM":    "1",
		"GIT_NO_REPLACE_OBJECTS": "1",
		"GIT_TERMINAL_PROMPT":    "0",
	}
	if len(gotGit) != len(wantGit) {
		t.Fatalf("unexpected hardened Git environment: %v", gotGit)
	}
	for key, want := range wantGit {
		if gotGit[key] != want {
			t.Errorf("%s=%q, want %q", key, gotGit[key], want)
		}
	}
}

func TestExecGitHTTPSOperationsIgnoreHostileEnclosingRepository(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	enclosing := t.TempDir()
	initCmd := exec.Command(git, "init", "--quiet", enclosing)
	if out, runErr := initCmd.CombinedOutput(); runErr != nil {
		t.Fatalf("git init: %v: %s", runErr, out)
	}
	marker := filepath.Join(t.TempDir(), "transport-helper-ran")
	helper := filepath.Join(t.TempDir(), "hostile-transport")
	if err = os.WriteFile(helper, []byte("#!/bin/sh\n: > \"$BREWNICLE_GIT_ATTACK_MARKER\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := "[protocol \"ext\"]\n\tallow = always\n[url \"ext::" + helper + "\"]\n\tinsteadOf = https://127.0.0.1:1/\n"
	if err = os.WriteFile(filepath.Join(enclosing, ".git", "config"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(enclosing); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)
	t.Setenv("BREWNICLE_GIT_ATTACK_MARKER", marker)
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext")

	remote := "https://127.0.0.1:1/repository.git"
	for _, invocation := range [][]string{
		{"ls-remote", "--symref", remote, "HEAD"},
		{"clone", "--bare", remote, filepath.Join(t.TempDir(), "clone.git")},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, runErr := (ExecGit{Binary: git}).Run(ctx, "", invocation...)
		cancel()
		if runErr == nil {
			t.Fatalf("unexpected success for %v", invocation)
		}
		if strings.Contains(strings.ToLower(runErr.Error()), "transport 'ext'") || strings.Contains(runErr.Error(), helper) {
			t.Fatalf("hostile enclosing repository rewrote official HTTPS remote for %v: %v", invocation, runErr)
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("hostile enclosing repository executed transport helper for %v: %v", invocation, statErr)
		}
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
