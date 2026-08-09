package history

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShallowRepositoryIsCompletedBeforeFullScan(t *testing.T) {
	git, e := exec.LookPath("git")
	if e != nil {
		t.Skip("git unavailable")
	}
	base := t.TempDir()
	src := filepath.Join(base, "src")
	remote := filepath.Join(base, "remote.git")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	_ = os.MkdirAll(filepath.Join(src, "Formula"), 0755)
	run(src, "init", "-q", "-b", "main")
	run(src, "config", "user.email", "x@y")
	run(src, "config", "user.name", "x")
	_ = os.WriteFile(filepath.Join(src, "Formula", "old.rb"), []byte("x"), 0644)
	run(src, "add", ".")
	run(src, "commit", "-qm", "old")
	_ = os.WriteFile(filepath.Join(src, "Formula", "new.rb"), []byte("x"), 0644)
	run(src, "add", ".")
	run(src, "commit", "-qm", "new")
	run(base, "clone", "-q", "--bare", src, remote)
	root := filepath.Join(base, "cache")
	_ = os.MkdirAll(root, 0700)
	run(base, "clone", "-q", "--bare", "--depth=1", "file://"+remote, filepath.Join(root, "core.git"))
	cache := Cache{Root: root, Git: ExecGit{Binary: git}, Scanner: Scanner{Git: git}, remotes: map[RepoKind]string{RepoCore: "file://" + remote}}
	update, e := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false)
	if e != nil {
		t.Fatal(e)
	}
	if update.State.Events["old"].IsZero() || update.State.Events["new"].IsZero() {
		t.Fatalf("truncated events: %v", update.State.Events)
	}
}
