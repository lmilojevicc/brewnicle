package history

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type countingGitRunner struct {
	inner GitRunner
	calls int
}

func (r *countingGitRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	r.calls++
	return r.inner.Run(ctx, dir, args...)
}

func TestCacheInitialCloneAndRefreshWithLocalRemote(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	base := t.TempDir()
	src := filepath.Join(base, "src")
	remote := filepath.Join(base, "remote.git")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v: %s", args, e, out)
		}
	}
	if err = os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	run(src, "init", "-q", "-b", "main")
	run(src, "config", "user.email", "test@example.com")
	run(src, "config", "user.name", "Test")
	os.MkdirAll(filepath.Join(src, "Formula"), 0755)
	os.WriteFile(filepath.Join(src, "Formula", "one.rb"), []byte("x"), 0644)
	run(src, "add", ".")
	run(src, "commit", "-qm", "one")
	run(base, "clone", "-q", "--bare", src, remote)
	cache := Cache{Root: filepath.Join(base, "cache"), Git: ExecGit{Binary: git}, Scanner: Scanner{Git: git}}
	events, e := cache.update(context.Background(), "core.git", remote, RepoCore, nil)
	if e != nil || events["one"].IsZero() {
		t.Fatal(events, e)
	}
	// Mutating cached origin must not redirect subsequent fetches. The cache
	// update always fetches from the explicit authoritative remote argument.
	maliciousSrc := filepath.Join(base, "malicious-src")
	maliciousRemote := filepath.Join(base, "malicious.git")
	if err = os.MkdirAll(filepath.Join(maliciousSrc, "Formula"), 0755); err != nil {
		t.Fatal(err)
	}
	run(maliciousSrc, "init", "-q", "-b", "main")
	run(maliciousSrc, "config", "user.email", "test@example.com")
	run(maliciousSrc, "config", "user.name", "Test")
	if err = os.WriteFile(filepath.Join(maliciousSrc, "Formula", "evil.rb"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run(maliciousSrc, "add", ".")
	run(maliciousSrc, "commit", "-qm", "evil")
	run(base, "clone", "-q", "--bare", maliciousSrc, maliciousRemote)
	run(base, "--git-dir", filepath.Join(cache.Root, "core.git"), "remote", "set-url", "origin", maliciousRemote)

	if err = os.WriteFile(filepath.Join(src, "Formula", "two.rb"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", ".")
	run(src, "commit", "-qm", "two")
	run(src, "push", "-q", remote, "main")
	events, e = cache.update(context.Background(), "core.git", remote, RepoCore, nil)
	if e != nil || events["two"].IsZero() {
		t.Fatal(events, e)
	}
	if _, ok := events["evil"]; ok {
		t.Fatal("fetch followed mutable origin")
	}
	if _, e = os.Stat(filepath.Join(cache.Root, "core.git")); e != nil {
		t.Fatal(e)
	}
}

func TestCacheRejectsCommitGraphsSymlinkBeforeRealFetch(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	base := t.TempDir()
	src := filepath.Join(base, "src")
	remote := filepath.Join(base, "remote.git")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			t.Fatalf("git %v: %v: %s", args, runErr, out)
		}
	}
	if err = os.MkdirAll(filepath.Join(src, "Formula"), 0755); err != nil {
		t.Fatal(err)
	}
	run(src, "init", "-q", "-b", "main")
	run(src, "config", "user.email", "test@example.com")
	run(src, "config", "user.name", "Test")
	if err = os.WriteFile(filepath.Join(src, "Formula", "one.rb"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", ".")
	run(src, "commit", "-qm", "one")
	run(base, "clone", "-q", "--bare", src, remote)

	cache := Cache{Root: filepath.Join(base, "cache"), Git: ExecGit{Binary: git}, Scanner: Scanner{Git: git}}
	if _, err = cache.update(context.Background(), "core.git", remote, RepoCore, nil); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(cache.Root, "core.git")
	run(base, "--git-dir", repo, "config", "fetch.writeCommitGraph", "true")

	outside := filepath.Join(base, "outside")
	if err = os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	commitGraphs := filepath.Join(repo, "objects", "info", "commit-graphs")
	if err = os.RemoveAll(commitGraphs); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, commitGraphs); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Make the authoritative remote advance so an unguarded fetch would have
	// work to do and, with fetch.writeCommitGraph enabled, could write through
	// the nested symlink.
	if err = os.WriteFile(filepath.Join(src, "Formula", "two.rb"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", ".")
	run(src, "commit", "-qm", "two")
	run(src, "push", "-q", remote, "main")

	counter := &countingGitRunner{inner: ExecGit{Binary: git}}
	cache.Git = counter
	if _, err = cache.update(context.Background(), "core.git", remote, RepoCore, nil); err == nil || !strings.Contains(err.Error(), "commit-graphs") {
		t.Fatalf("expected commit-graphs validation failure, got %v", err)
	}
	if counter.calls != 0 {
		t.Fatalf("git ran %d times before nested commit-graphs validation", counter.calls)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("git wrote outside cache: %v", entries)
	}
}
