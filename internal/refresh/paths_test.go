package refresh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	p, e := ResolvePaths(root)
	if e != nil || p.Index != filepath.Join(root, "index.db") || p.Git != filepath.Join(root, "git") {
		t.Fatal(p, e)
	}
}

func TestResolvePathsRejectsSymlinkRoot(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "cache")
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolvePaths(root); err == nil {
		t.Fatal("accepted symlink cache root")
	}
}
