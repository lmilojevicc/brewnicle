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

func TestParseDefaultBranch(t *testing.T) {
	if got, err := parseDefaultBranch("ref: refs/heads/main\tHEAD\nabc\tHEAD\n"); err != nil || got != "refs/heads/main" {
		t.Fatal(got, err)
	}
	for _, raw := range []string{"refs/heads/main", "ref: refs/tags/x\tHEAD\n", "ref: refs/heads/../x\tHEAD\n"} {
		if _, err := parseDefaultBranch(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestUnsupportedFilter(t *testing.T) {
	if !unsupportedFilter("warning: filtering not recognized by server") || unsupportedFilter("network down") {
		t.Fatal()
	}
}

type gitRunnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f gitRunnerFunc) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return f(ctx, dir, args...)
}

type scannerFunc func(context.Context, string, string, RepoKind) (map[string]time.Time, error)

func (f scannerFunc) Scan(ctx context.Context, dir, ref string, kind RepoKind) (map[string]time.Time, error) {
	return f(ctx, dir, ref, kind)
}

func TestInitialCloneScanFailureIsNotPromoted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if args[0] == "clone" {
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		}
		return nil, errors.New("unexpected git invocation")
	})
	cache := Cache{
		Root: root,
		Git:  runner,
		Scanner: scannerFunc(func(context.Context, string, string, RepoKind) (map[string]time.Time, error) {
			return nil, errors.New("malformed history")
		}),
	}
	if _, err := cache.update(context.Background(), "core.git", CoreRemote, RepoCore, nil); err == nil {
		t.Fatal("expected scan failure")
	}
	if _, err := os.Lstat(filepath.Join(root, "core.git")); !os.IsNotExist(err) {
		t.Fatalf("invalid clone was promoted: %v", err)
	}
}

func TestCacheRejectsSymlinkRootAndRepositoryBeforeGit(t *testing.T) {
	calls := 0
	runner := gitRunnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("must not run")
	})
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.MkdirAll(realRoot, 0700); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(base, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cache := Cache{Root: rootLink, Git: runner}
	if _, err := cache.update(context.Background(), "core.git", CoreRemote, RepoCore, nil); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("root symlink accepted: %v", err)
	}

	outside := filepath.Join(base, "outside.git")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(realRoot, "core.git")); err != nil {
		t.Fatal(err)
	}
	cache.Root = realRoot
	if _, err := cache.update(context.Background(), "core.git", CoreRemote, RepoCore, nil); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("repository symlink accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("ran git %d times before rejecting symlink", calls)
	}
}

func TestCacheRejectsAncestorAndNestedWriteSymlinksBeforeGit(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner := gitRunnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("must not run")
	})

	configured := filepath.Join(base, "configured")
	if err := os.Symlink(outside, configured); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cache := Cache{Boundary: base, Root: filepath.Join(configured, "git"), Git: runner}
	if _, err := cache.update(context.Background(), "core.git", CoreRemote, RepoCore, nil); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("ancestor symlink accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("ran git %d times before rejecting ancestor symlink", calls)
	}

	for _, nested := range []string{"refs", "objects"} {
		t.Run(nested, func(t *testing.T) {
			root := filepath.Join(base, "cache-"+nested)
			repo := filepath.Join(root, "core.git")
			if err := os.MkdirAll(repo, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(repo, nested)); err != nil {
				t.Fatal(err)
			}
			before := calls
			cache = Cache{Boundary: base, Root: root, Git: runner}
			if _, err := cache.update(context.Background(), "core.git", CoreRemote, RepoCore, nil); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
				t.Fatalf("nested %s symlink accepted: %v", nested, err)
			}
			if calls != before {
				t.Fatalf("ran git before rejecting nested %s symlink", nested)
			}
		})
	}
}

func TestCacheRejectsCommitGraphWritePathsBeforeGit(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
	}{
		{
			name: "split directory symlink",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(repo, "objects", "info"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(repo, "objects", "info", "commit-graphs")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
		{
			name: "split directory wrong type",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(repo, "objects", "info"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, "objects", "info", "commit-graphs"), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "monolithic file symlink",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(repo, "objects", "info"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "commit-graph"), filepath.Join(repo, "objects", "info", "commit-graph")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
		{
			name: "split child symlink",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				dir := filepath.Join(repo, "objects", "info", "commit-graphs")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "commit-graph-chain"), filepath.Join(dir, "commit-graph-chain")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(base, strings.ReplaceAll(tt.name, " ", "-"))
			repo := filepath.Join(root, "core.git")
			if err := os.MkdirAll(repo, 0700); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, repo)

			calls := 0
			cache := Cache{
				Boundary: base,
				Root:     root,
				Git: gitRunnerFunc(func(context.Context, string, ...string) ([]byte, error) {
					calls++
					return nil, errors.New("must not run")
				}),
			}
			if _, err := cache.update(context.Background(), "core.git", CoreRemote, RepoCore, nil); err == nil {
				t.Fatal("unsafe commit-graph path accepted")
			}
			if calls != 0 {
				t.Fatalf("ran git %d times before rejecting unsafe commit-graph path", calls)
			}
		})
	}
}

func TestExistingCacheFetchUsesExplicitRemote(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "core.git")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	var fetchArgs []string
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if len(args) > 2 && args[2] == "fetch" {
			fetchArgs = append([]string(nil), args...)
			return nil, nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		return nil, errors.New("unexpected git invocation")
	})
	cache := Cache{
		Root: root,
		Git:  runner,
		Scanner: scannerFunc(func(context.Context, string, string, RepoKind) (map[string]time.Time, error) {
			return map[string]time.Time{"ok": time.Unix(1, 0)}, nil
		}),
	}
	if _, err := cache.update(context.Background(), "core.git", CoreRemote, RepoCore, nil); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fetchArgs, " ")
	if !strings.Contains(joined, CoreRemote) || strings.Contains(joined, " origin ") {
		t.Fatalf("fetch did not enforce explicit remote: %q", joined)
	}
}
