package history

import (
	"context"
	"errors"
	"fmt"
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

type scannerFunc func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error)

func (f scannerFunc) Scan(ctx context.Context, dir string, rev Revision, kind RepoKind) (map[string]time.Time, error) {
	return f(ctx, dir, rev, kind)
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
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository" {
			return []byte("false\n"), nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		return nil, errors.New("unexpected git invocation")
	})
	cache := Cache{
		Root: root,
		Git:  runner,
		Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
			return nil, errors.New("malformed history")
		}),
	}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil {
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
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "real directory") {
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
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "real directory") {
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
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "real directory") {
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
			if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
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
			if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil {
				t.Fatal("unsafe commit-graph path accepted")
			}
			if calls != 0 {
				t.Fatalf("ran git %d times before rejecting unsafe commit-graph path", calls)
			}
		})
	}
}

func TestStillShallowAfterUnshallowRunsOneRecoveryClone(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "core.git")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	clones, shallowChecks := 0, 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if args[0] == "clone" {
			clones++
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		}
		if len(args) > 2 && args[2] == "for-each-ref" {
			return nil, nil
		}
		if len(args) > 2 && args[2] == "fetch" {
			return nil, nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository" {
			shallowChecks++
			if shallowChecks <= 2 {
				return []byte("true\n"), nil
			}
			return []byte("false\n"), nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		return nil, errors.New("unexpected git invocation")
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return map[string]time.Time{"recovered": time.Unix(1, 0)}, nil
	})}
	update, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false)
	if err != nil || !update.RecoveredRepository || update.State.Events["recovered"].IsZero() {
		t.Fatal(update, err)
	}
	if clones != 1 {
		t.Fatalf("recovery clones=%d", clones)
	}
}

func TestUnshallowAuthenticationFailureDoesNotRecoveryClone(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "core.git")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	clones := 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if len(args) > 2 && args[2] == "for-each-ref" {
			return nil, nil
		}
		if len(args) > 2 && args[2] == "fetch" {
			for _, arg := range args {
				if arg == "--unshallow" {
					return nil, &CommandError{Status: 128, Err: errors.New("authentication failed"), Output: "authentication failed"}
				}
			}
			return nil, nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository" {
			return []byte("true\n"), nil
		}
		if args[0] == "clone" {
			clones++
			return nil, nil
		}
		return nil, errors.New("unexpected git invocation")
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return nil, errors.New("must not scan")
	})}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatal(err)
	}
	if clones != 0 {
		t.Fatalf("recovery clones=%d", clones)
	}
}

func TestReconcileFirstMigrationUsesRepositoryHashWidth(t *testing.T) {
	for _, width := range []int{40, 64} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "core.git"), 0700); err != nil {
				t.Fatal(err)
			}
			var got []string
			cache := Cache{Root: root, Git: gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				got = append([]string(nil), args...)
				return nil, nil
			})}
			oid := strings.Repeat("a", width)
			prepared := Prepared{Updates: [2]RepoUpdate{{State: RepoState{Repo: RepoCore, TipOID: oid}}, {State: RepoState{Repo: RepoCask, TipOID: oid}}}}
			if err := os.MkdirAll(filepath.Join(root, "cask.git"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := cache.ReconcilePublished(context.Background(), prepared); err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 || got[len(got)-1] != strings.Repeat("0", width) {
				t.Fatalf("args=%v", got)
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
		if len(args) > 2 && args[2] == "for-each-ref" {
			return nil, nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository" {
			return []byte("false\n"), nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		return nil, errors.New("unexpected git invocation")
	})
	cache := Cache{
		Root: root,
		Git:  runner,
		Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
			return map[string]time.Time{"ok": time.Unix(1, 0)}, nil
		}),
	}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fetchArgs, " ")
	if !strings.Contains(joined, CoreRemote) || strings.Contains(joined, " origin ") {
		t.Fatalf("fetch did not enforce explicit remote: %q", joined)
	}
}

func recoveryFixture(t *testing.T, root string, events map[string]time.Time, ops *cacheOps) Cache {
	t.Helper()
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "clone" {
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository" {
			return []byte("false\n"), nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected git args %v", args)
	})
	return Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return events, nil
	}), ops: ops}
}

func TestRecoveryValidatesStateBeforePromotion(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "core.git")
	if err := os.MkdirAll(canonical, 0700); err != nil {
		t.Fatal(err)
	}
	cache := recoveryFixture(t, root, map[string]time.Time{"bad": time.Unix(0, 0)}, nil)
	if _, err := cache.recover(context.Background(), canonical, CoreRemote, "refs/heads/main", RepoCore, nil); err == nil {
		t.Fatal("invalid state promoted")
	}
	if info, err := os.Stat(canonical); err != nil || !info.IsDir() {
		t.Fatal(info, err)
	}
	matches, _ := filepath.Glob(canonical + ".backup-*")
	if len(matches) != 0 {
		t.Fatal(matches)
	}
}

func TestRecoveryPromotionFailureRestoresCanonical(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "core.git")
	if err := os.MkdirAll(canonical, 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ops := &cacheOps{rename: func(from, to string) error {
		calls++
		if calls == 2 {
			return errors.New("promote")
		}
		return os.Rename(from, to)
	}}
	cache := recoveryFixture(t, root, map[string]time.Time{"ok": time.Unix(1, 0)}, ops)
	_, err := cache.recover(context.Background(), canonical, CoreRemote, "refs/heads/main", RepoCore, nil)
	if err == nil || !strings.Contains(err.Error(), "restored from") {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(canonical); statErr != nil {
		t.Fatal(statErr)
	}
}

func TestRecoveryRestoreFailureExposesBackup(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "core.git")
	if err := os.MkdirAll(canonical, 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ops := &cacheOps{rename: func(from, to string) error {
		calls++
		if calls >= 2 {
			return fmt.Errorf("rename-%d", calls)
		}
		return os.Rename(from, to)
	}}
	cache := recoveryFixture(t, root, map[string]time.Time{"ok": time.Unix(1, 0)}, ops)
	_, err := cache.recover(context.Background(), canonical, CoreRemote, "refs/heads/main", RepoCore, nil)
	matches, _ := filepath.Glob(canonical + ".backup-*")
	if err == nil || !strings.Contains(err.Error(), "restore repository from") || len(matches) != 1 || !strings.Contains(err.Error(), matches[0]) {
		t.Fatal(err, matches)
	}
	if _, statErr := os.Stat(canonical); !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
}

func TestRepairRecoveryBackups(t *testing.T) {
	t.Run("single restores", func(t *testing.T) {
		root := t.TempDir()
		backup := filepath.Join(root, "core.git.backup-1")
		if err := os.MkdirAll(backup, 0700); err != nil {
			t.Fatal(err)
		}
		cache := Cache{Root: root}
		if err := cache.repairRecoveryBackups("core.git"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "core.git")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ambiguous refuses", func(t *testing.T) {
		root := t.TempDir()
		for _, n := range []string{"core.git.backup-1", "core.git.backup-2"} {
			if err := os.MkdirAll(filepath.Join(root, n), 0700); err != nil {
				t.Fatal(err)
			}
		}
		cache := Cache{Root: root}
		if err := cache.repairRecoveryBackups("core.git"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatal(err)
		}
	})
	t.Run("stale cleanup failure is surfaced", func(t *testing.T) {
		root := t.TempDir()
		for _, n := range []string{"core.git", "core.git.backup-1"} {
			if err := os.MkdirAll(filepath.Join(root, n), 0700); err != nil {
				t.Fatal(err)
			}
		}
		cache := Cache{Root: root, ops: &cacheOps{removeAll: func(string) error { return errors.New("cleanup") }}}
		if err := cache.repairRecoveryBackups("core.git"); err == nil || !strings.Contains(err.Error(), "cleanup") {
			t.Fatal(err)
		}
	})
}

func TestLargeAggregateOneCommitUsesOnlyExactRange(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core.git"), 0700); err != nil {
		t.Fatal(err)
	}
	oldOID, newOID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	events := make(map[string]time.Time, 10000)
	for i := 0; i < 10000; i++ {
		events[fmt.Sprintf("pkg-%05d", i)] = time.Unix(int64(i+1), 0)
	}
	previous := RepoState{Repo: RepoCore, RemoteURL: CoreRemote, BranchRef: "refs/heads/main", TipOID: oldOID, AlgorithmVersion: AlgorithmVersion, Complete: true, Events: events}
	var scanned Revision
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if len(args) > 2 && args[2] == "for-each-ref" {
			return []byte(oldOID + "\n"), nil
		}
		if len(args) > 3 && args[2] == "cat-file" && args[3] == "-e" {
			return nil, nil
		}
		if len(args) > 3 && args[2] == "cat-file" && args[3] == "-t" {
			return []byte("commit\n"), nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		if len(args) > 2 && args[2] == "fetch" {
			return nil, nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(newOID + "\n"), nil
		}
		if len(args) > 3 && args[2] == "merge-base" {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected args %v", args)
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(_ context.Context, _ string, rev Revision, _ RepoKind) (map[string]time.Time, error) {
		scanned = rev
		return map[string]time.Time{"new-package": time.Unix(20000, 0)}, nil
	})}
	update, err := cache.prepareRepo(context.Background(), RepoCore, previous, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.FromOID != oldOID || scanned.ToOID != newOID || update.Mode != ScanIncremental || len(update.State.Events) != 10001 {
		t.Fatalf("scan=%+v mode=%s events=%d", scanned, update.Mode, len(update.State.Events))
	}
}

func TestRecoveryFirstRenameFailureLeavesCanonical(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "core.git")
	if err := os.MkdirAll(canonical, 0700); err != nil {
		t.Fatal(err)
	}
	ops := &cacheOps{rename: func(string, string) error { return errors.New("first") }}
	cache := recoveryFixture(t, root, map[string]time.Time{"ok": time.Unix(1, 0)}, ops)
	if _, err := cache.recover(context.Background(), canonical, CoreRemote, "refs/heads/main", RepoCore, nil); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(canonical); err != nil {
		t.Fatal(err)
	}
}

func TestInitialCloneInvalidStateLeavesCanonicalAbsent(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "core.git")
	cache := recoveryFixture(t, root, map[string]time.Time{"bad": time.Unix(0, 0)}, nil)
	if _, err := cache.cloneAndPromote(context.Background(), repo, CoreRemote, "refs/heads/main", RepoCore, nil); err == nil {
		t.Fatal("invalid state accepted")
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestReconcileRepairsRefsIndependentlyAfterPartialFailure(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"core.git", "cask.git"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	coreOID, caskOID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	prepared := Prepared{Updates: [2]RepoUpdate{
		{State: RepoState{Repo: RepoCore, TipOID: coreOID}},
		{State: RepoState{Repo: RepoCask, TipOID: caskOID}},
	}}
	failedCask := true
	calls := map[RepoKind]int{}
	cache := Cache{Root: root, Git: gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 3 && args[2] == "update-ref" {
			repo := RepoCore
			if strings.Contains(args[1], "cask.git") {
				repo = RepoCask
			}
			calls[repo]++
			if repo == RepoCask && failedCask {
				return nil, errors.New("cask cas")
			}
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected args %v", args)
	})}
	if err := cache.ReconcilePublished(context.Background(), prepared); err == nil {
		t.Fatal("partial failure not reported")
	}
	failedCask = false
	prepared.Updates[0].PublishedExpectation.ExpectedOID = coreOID
	if err := cache.ReconcilePublished(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	if calls[RepoCore] != 2 || calls[RepoCask] != 2 {
		t.Fatal(calls)
	}
}

func TestCacheLaunchFailureDoesNotTriggerRecovery(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core.git"), 0700); err != nil {
		t.Fatal(err)
	}
	oid := strings.Repeat("a", 40)
	clones := 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if args[0] == "clone" {
			clones++
			return nil, nil
		}
		if len(args) > 2 && args[2] == "for-each-ref" {
			return []byte(oid + "\n"), nil
		}
		if len(args) > 3 && args[2] == "cat-file" && args[3] == "-e" {
			return nil, newCommandError(args, nil, errors.New("executable not found"))
		}
		return nil, fmt.Errorf("unexpected args %v", args)
	})
	previous := RepoState{Repo: RepoCore, RemoteURL: CoreRemote, BranchRef: "refs/heads/main", TipOID: oid, AlgorithmVersion: AlgorithmVersion, Complete: true, Events: map[string]time.Time{"old": time.Unix(1, 0)}}
	cache := Cache{Root: root, Git: runner}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, previous, nil, false); err == nil || !strings.Contains(err.Error(), "git cat-file") {
		t.Fatal(err)
	}
	if clones != 0 {
		t.Fatalf("launch failure triggered %d recovery clones", clones)
	}
}

func TestCleanupAbandonedHistoryCandidates(t *testing.T) {
	for _, canonicalPresent := range []bool{false, true} {
		t.Run(fmt.Sprintf("canonical-%v", canonicalPresent), func(t *testing.T) {
			root := t.TempDir()
			if canonicalPresent {
				if err := os.MkdirAll(filepath.Join(root, "core.git"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{".core.git-tmp-123", ".core.git-recovery-456", ".core.git-tmp-not-owned", "unrelated"} {
				if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cache := Cache{Root: root}
			if err := cache.repairRecoveryBackups("core.git"); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{".core.git-tmp-123", ".core.git-recovery-456"} {
				if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatalf("abandoned %s retained: %v", name, err)
				}
			}
			for _, name := range []string{".core.git-tmp-not-owned", "unrelated"} {
				if _, err := os.Stat(filepath.Join(root, name)); err != nil {
					t.Fatalf("unrelated %s removed: %v", name, err)
				}
			}
		})
	}
}

func TestCleanupAbandonedHistoryCandidatesRefusesUnsafeAndSurfacesFailure(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		path := filepath.Join(root, ".core.git-tmp-123")
		if err := os.Symlink(outside, path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		cache := Cache{Root: root}
		if err := cache.repairRecoveryBackups("core.git"); err == nil || !strings.Contains(err.Error(), "not a real directory") {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("unsafe path was removed", err)
		}
	})
	t.Run("nested symlink", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		path := filepath.Join(root, ".core.git-recovery-123")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(path, "refs")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		cache := Cache{Root: root}
		if err := cache.repairRecoveryBackups("core.git"); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("unsafe path was removed", err)
		}
	})
	t.Run("remove failure", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, ".core.git-tmp-123")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		cache := Cache{Root: root, ops: &cacheOps{removeAll: func(string) error { return errors.New("cleanup") }}}
		if err := cache.repairRecoveryBackups("core.git"); err == nil || !strings.Contains(err.Error(), "cleanup") {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("failed cleanup removed path", err)
		}
	})
}

func TestFetchedAheadUsesAuthoritativeDatabaseCursor(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core.git"), 0700); err != nil {
		t.Fatal(err)
	}
	oldOID, fetchedAhead, newOID := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	previous := RepoState{Repo: RepoCore, RemoteURL: CoreRemote, BranchRef: "refs/heads/main", TipOID: oldOID, AlgorithmVersion: AlgorithmVersion, Complete: true, Events: map[string]time.Time{"old": time.Unix(1, 0)}}
	var scanned Revision
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if len(args) > 2 && args[2] == "for-each-ref" {
			return []byte(fetchedAhead + "\n"), nil
		}
		if len(args) > 3 && args[2] == "cat-file" && args[3] == "-e" {
			return nil, nil
		}
		if len(args) > 3 && args[2] == "cat-file" && args[3] == "-t" {
			return []byte("commit\n"), nil
		}
		if len(args) > 2 && (args[2] == "update-ref" || args[2] == "fetch") {
			return nil, nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(newOID + "\n"), nil
		}
		if len(args) > 3 && args[2] == "merge-base" {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected args %v", args)
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(_ context.Context, _ string, rev Revision, _ RepoKind) (map[string]time.Time, error) {
		scanned = rev
		return map[string]time.Time{"new": time.Unix(2, 0)}, nil
	})}
	update, err := cache.prepareRepo(context.Background(), RepoCore, previous, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.FromOID != oldOID || scanned.ToOID != newOID || update.BaseOID != oldOID {
		t.Fatalf("scan=%+v update=%+v", scanned, update)
	}
}

func TestInitialCloneProgressIsNotMigration(t *testing.T) {
	root := t.TempDir()
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if args[0] == "clone" {
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository" {
			return []byte("false\n"), nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected args %v", args)
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return map[string]time.Time{"pkg": time.Unix(1, 0)}, nil
	})}
	var progress []string
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, func(detail string) { progress = append(progress, detail) }, false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(progress, "\n")
	if !strings.Contains(joined, "initial clone and full-history setup") || strings.Contains(joined, "migration") {
		t.Fatal(progress)
	}
}
