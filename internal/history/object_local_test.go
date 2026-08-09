package history

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRawMissingAndWrongTypeObjectsSelectFallback(t *testing.T) {
	git, e := exec.LookPath("git")
	if e != nil {
		t.Skip("git unavailable")
	}
	repo := filepath.Join(t.TempDir(), "repo.git")
	cmd := exec.Command(git, "init", "--bare", "-q", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	cache := Cache{Git: ExecGit{Binary: git}}
	ok, e := cache.objectIsCommit(context.Background(), repo, strings.Repeat("a", 40))
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	blobCmd := exec.Command(git, "--git-dir", repo, "hash-object", "-w", "--stdin")
	blobCmd.Stdin = strings.NewReader("blob")
	out, e := blobCmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	blob := strings.TrimSpace(string(out))
	ok, e = cache.objectIsCommit(context.Background(), repo, blob)
	if e != nil || ok {
		t.Fatal(ok, e)
	}
}
