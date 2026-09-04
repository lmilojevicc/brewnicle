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

func TestInitialCloneScanFailureKeepsReusableDownloadWithoutPromotion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	clones := 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ls-remote" {
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		}
		if args[0] == "clone" {
			clones++
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository" {
			return []byte("false\n"), nil
		}
		if len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify" {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if len(args) > 3 && args[2] == "config" {
			return []byte(CoreRemote + "\n"), nil
		}
		if len(args) > 3 && args[2] == "symbolic-ref" {
			return []byte("refs/heads/main\n"), nil
		}
		if gitOperation(args) == "update-ref" || gitOperation(args) == "fetch" {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected git invocation %v", args)
	})
	scanErr := true
	cache := Cache{
		Root: root,
		Git:  runner,
		Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
			if scanErr {
				return nil, errors.New("malformed history")
			}
			return map[string]time.Time{"pkg": time.Unix(1, 0)}, nil
		}),
	}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil {
		t.Fatal("expected scan failure")
	}
	if _, err := os.Lstat(filepath.Join(root, "core.git")); !os.IsNotExist(err) {
		t.Fatalf("failed history was promoted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "core.git.bootstrap")); err != nil {
		t.Fatalf("completed download was not checkpointed: %v", err)
	}
	scanErr = false
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err != nil {
		t.Fatal(err)
	}
	if clones != 1 {
		t.Fatalf("retry cloned %d times", clones)
	}
	if _, err := os.Stat(filepath.Join(root, "core.git")); err != nil {
		t.Fatalf("validated retry was not promoted: %v", err)
	}
}

func TestInitialCloneCancellationKeepsReusableDownload(t *testing.T) {
	root := t.TempDir()
	clones := 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch {
		case args[0] == "ls-remote":
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		case args[0] == "clone":
			clones++
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository":
			return []byte("false\n"), nil
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify":
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case len(args) > 3 && args[2] == "config":
			return []byte(CoreRemote + "\n"), nil
		case len(args) > 3 && args[2] == "symbolic-ref":
			return []byte("refs/heads/main\n"), nil
		case (gitOperation(args) == "update-ref" || gitOperation(args) == "fetch"):
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected git invocation %v", args)
		}
	})
	cancelFirst := true
	ctx, cancel := context.WithCancel(context.Background())
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(scanCtx context.Context, _ string, _ Revision, _ RepoKind) (map[string]time.Time, error) {
		if cancelFirst {
			cancel()
			return nil, scanCtx.Err()
		}
		return map[string]time.Time{"pkg": time.Unix(1, 0)}, nil
	})}
	if _, err := cache.prepareRepo(ctx, RepoCore, RepoState{}, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "core.git.bootstrap")); err != nil {
		t.Fatal(err)
	}
	cancelFirst = false
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err != nil {
		t.Fatal(err)
	}
	if clones != 1 {
		t.Fatalf("retry cloned %d times", clones)
	}
}

func TestInvalidButHealthyPendingCloneIsPreserved(t *testing.T) {
	root := t.TempDir()
	pending := filepath.Join(root, "core.git.bootstrap")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	clones, configChecks := 0, 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch {
		case args[0] == "ls-remote":
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		case args[0] == "clone":
			clones++
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository":
			return []byte("false\n"), nil
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify":
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case len(args) > 3 && args[2] == "config":
			configChecks++
			if configChecks == 1 {
				return []byte("https://example.test/wrong.git\n"), nil
			}
			return []byte(CoreRemote + "\n"), nil
		case len(args) > 3 && args[2] == "symbolic-ref":
			return []byte("refs/heads/main\n"), nil
		case len(args) > 2 && args[2] == "update-ref":
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected git invocation %v", args)
		}
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return map[string]time.Time{"pkg": time.Unix(1, 0)}, nil
	})}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "unexpected remote") {
		t.Fatal(err)
	}
	if clones != 0 || configChecks != 1 {
		t.Fatalf("clones=%d config checks=%d", clones, configChecks)
	}
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("healthy checkpoint was removed: %v", err)
	}
}

func TestCorruptPendingCloneIsReclonedAfterGitScanFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core.git.bootstrap"), 0700); err != nil {
		t.Fatal(err)
	}
	clones, probes := 0, 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch {
		case args[0] == "ls-remote":
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		case args[0] == "clone":
			clones++
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository":
			return []byte("false\n"), nil
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify":
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case len(args) > 3 && args[2] == "config":
			return []byte(CoreRemote + "\n"), nil
		case len(args) > 3 && args[2] == "symbolic-ref":
			return []byte("refs/heads/main\n"), nil
		case (gitOperation(args) == "update-ref" || gitOperation(args) == "fetch"):
			return nil, nil
		case len(args) > 2 && args[2] == "fsck":
			probes++
			return nil, &CommandError{Args: args, Status: 2, Err: errors.New("connectivity"), Output: "broken link from commit to missing tree"}
		default:
			return nil, fmt.Errorf("unexpected git invocation %v", args)
		}
	})
	scans := 0
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		scans++
		if scans == 1 {
			return nil, &CommandError{Args: []string{"log"}, Status: 128, Err: errors.New("bad object"), Output: "bad object"}
		}
		return map[string]time.Time{"pkg": time.Unix(1, 0)}, nil
	})}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err != nil {
		t.Fatal(err)
	}
	if clones != 1 || scans != 2 || probes != 1 {
		t.Fatalf("clones=%d scans=%d probes=%d", clones, scans, probes)
	}
	if _, err := os.Stat(filepath.Join(root, "core.git")); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafePendingSymlinkIsRejectedWithoutRemoval(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	pending := filepath.Join(root, "core.git.bootstrap")
	if err := os.Symlink(outside, pending); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	clones := 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch {
		case args[0] == "ls-remote":
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		case args[0] == "clone":
			clones++
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--is-shallow-repository":
			return []byte("false\n"), nil
		case len(args) > 3 && args[2] == "rev-parse" && args[3] == "--verify":
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case len(args) > 3 && args[2] == "config":
			return []byte(CoreRemote + "\n"), nil
		case len(args) > 3 && args[2] == "symbolic-ref":
			return []byte("refs/heads/main\n"), nil
		case len(args) > 2 && args[2] == "update-ref":
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected git invocation %v", args)
		}
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return map[string]time.Time{"pkg": time.Unix(1, 0)}, nil
	})}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatal(err)
	}
	if clones != 0 {
		t.Fatalf("clones=%d", clones)
	}
	if info, err := os.Stat(outside); err != nil || !info.IsDir() {
		t.Fatal("symlink target was changed", info, err)
	}
	if info, err := os.Lstat(pending); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("unsafe checkpoint was not preserved", info, err)
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

func TestRepositoryConfigRejectsTransportExecutionAndIncludeKeys(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
	}{
		{"url rewrite", "url.file:///tmp/substituted.insteadOf", "https://github.com/"},
		{"credential helper", "credential.helper", "!/tmp/credential-helper"},
		{"config include", "include.path", "/tmp/other-config"},
		{"conditional include", "includeIf.gitdir:/tmp/.path", "/tmp/other-config"},
		{"hooks path", "core.hooksPath", "/tmp/hooks"},
		{"external transport", "protocol.ext.allow", "always"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newGitFixture(t)
			fixture.run(nil, "config", "--local", "--unset-all", "user.email")
			fixture.run(nil, "config", "--local", "--unset-all", "user.name")
			fixture.run(nil, "config", "--local", tc.key, tc.value)
			cache := Cache{Git: ExecGit{Binary: fixture.bin}}
			if err := cache.validateRepositoryConfig(context.Background(), filepath.Join(fixture.dir, ".git"), ""); err == nil || !strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("dangerous config accepted: %v", err)
			}
		})
	}
}

func TestUnsafePendingURLRewriteIsRejectedAndPreserved(t *testing.T) {
	root := t.TempDir()
	pending := filepath.Join(root, "core.git.bootstrap")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "config"), []byte("[url \"file:///tmp/substituted\"]\n\tinsteadOf = https://github.com/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fetches, clones, probes := 0, 0, 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch gitOperation(args) {
		case "config":
			return []byte("url.file:///tmp/substituted.insteadof\x00"), nil
		case "fetch":
			fetches++
			return nil, errors.New("must not fetch through unsafe config")
		case "clone":
			clones++
			return nil, errors.New("must not clone")
		case "fsck":
			probes++
			return nil, &CommandError{Args: args, Status: 2, Err: errors.New("connectivity"), Output: "broken link from commit to missing tree"}
		default:
			return nil, fmt.Errorf("unexpected git invocation %v", args)
		}
	})
	cache := Cache{Root: root, Git: runner}
	if _, _, _, err := cache.prepareBootstrapRepository(context.Background(), CoreRemote, "refs/heads/main", RepoCore, nil); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatal(err)
	}
	if fetches != 0 || clones != 0 || probes != 0 {
		t.Fatalf("fetches=%d clones=%d probes=%d", fetches, clones, probes)
	}
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("unsafe checkpoint was not preserved: %v", err)
	}
}

func TestUnsafeHookConfigIsRejectedBeforeReferenceMutation(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.run(nil, "config", "--local", "--unset-all", "user.email")
	fixture.run(nil, "config", "--local", "--unset-all", "user.name")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooks, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture.run(nil, "config", "--local", "core.hooksPath", hooks)
	root := t.TempDir()
	if err := os.Rename(filepath.Join(fixture.dir, ".git"), filepath.Join(root, "core.git")); err != nil {
		t.Fatal(err)
	}
	cache := Cache{Root: root, Git: ExecGit{Binary: fixture.bin}}
	prepared := Prepared{Updates: [2]RepoUpdate{{State: RepoState{Repo: RepoCore, TipOID: strings.Repeat("a", 40)}}}}
	if err := cache.ReconcilePublished(context.Background(), prepared); err == nil || !strings.Contains(err.Error(), "unsafe repository config") {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("reference hook executed: %v", err)
	}
}

func TestRepositoryConfigRejectsAlternateObjectStores(t *testing.T) {
	fixture := newGitFixture(t)
	alternates := filepath.Join(fixture.dir, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte("/tmp/external-objects\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := Cache{Git: ExecGit{Binary: fixture.bin}}
	if err := cache.validateRepositoryConfig(context.Background(), filepath.Join(fixture.dir, ".git"), ""); err == nil || !strings.Contains(err.Error(), "alternate object store") {
		t.Fatal(err)
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

func TestCorruptPendingFetchIsDiscardedAndReclonedOnce(t *testing.T) {
	root := t.TempDir()
	pending := filepath.Join(root, "core.git.bootstrap")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	clones, fetches, probes := 0, 0, 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch gitOperation(args) {
		case "ls-remote":
			return []byte("ref: refs/heads/main\tHEAD\n"), nil
		case "clone":
			clones++
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		case "fetch":
			fetches++
			return nil, &CommandError{Args: args, Status: 128, Err: errors.New("missing blob"), Output: "fatal: missing blob aaaa"}
		case "fsck":
			probes++
			return nil, &CommandError{Args: args, Status: 2, Err: errors.New("connectivity"), Output: "broken link from tree to missing blob"}
		case "config":
			return []byte(CoreRemote + "\n"), nil
		case "symbolic-ref":
			return []byte("refs/heads/main\n"), nil
		case "rev-parse":
			if strings.Contains(strings.Join(args, " "), "--is-shallow-repository") {
				return []byte("false\n"), nil
			}
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case "update-ref":
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected git invocation %v", args)
		}
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return map[string]time.Time{"pkg": time.Unix(1, 0)}, nil
	})}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err != nil {
		t.Fatal(err)
	}
	if clones != 1 || fetches != 1 || probes != 1 {
		t.Fatalf("clones=%d fetches=%d probes=%d", clones, fetches, probes)
	}
	if _, err := os.Stat(filepath.Join(root, "core.git")); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalFetchUpdateCorruptionRunsOneRecoveryClone(t *testing.T) {
	for _, stage := range []string{"fetch", "fetched-ref", "unshallow"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			canonical := filepath.Join(root, "core.git")
			if err := os.MkdirAll(canonical, 0700); err != nil {
				t.Fatal(err)
			}
			clones, probes := 0, 0
			runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				op := gitOperation(args)
				joined := strings.Join(args, " ")
				switch op {
				case "ls-remote":
					return []byte("ref: refs/heads/main\tHEAD\n"), nil
				case "clone":
					clones++
					return nil, os.MkdirAll(args[len(args)-1], 0700)
				case "for-each-ref":
					return nil, nil
				case "fetch":
					if strings.Contains(joined, canonical) && (stage == "fetch" || stage == "unshallow" && strings.Contains(joined, "--unshallow")) {
						return nil, &CommandError{Args: args, Status: 128, Err: errors.New("missing object"), Output: "fatal: missing blob aaaa"}
					}
					return nil, nil
				case "fsck":
					probes++
					return nil, &CommandError{Args: args, Status: 2, Err: errors.New("connectivity"), Output: "broken link from commit to missing tree"}
				case "rev-parse":
					if strings.Contains(joined, "--is-shallow-repository") {
						if stage == "unshallow" && strings.Contains(joined, canonical) {
							return []byte("true\n"), nil
						}
						return []byte("false\n"), nil
					}
					if stage == "fetched-ref" && strings.Contains(joined, canonical) && strings.Contains(joined, FetchedRef) {
						return nil, &CommandError{Args: args, Status: 128, Err: errors.New("bad object"), Output: "fatal: invalid sha1 pointer"}
					}
					return []byte(strings.Repeat("a", 40) + "\n"), nil
				case "config":
					return []byte(CoreRemote + "\n"), nil
				case "symbolic-ref":
					return []byte("refs/heads/main\n"), nil
				case "update-ref":
					return nil, nil
				default:
					return nil, fmt.Errorf("unexpected git invocation %v", args)
				}
			})
			cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
				return map[string]time.Time{"recovered": time.Unix(1, 0)}, nil
			})}
			update, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if !update.RecoveredRepository || clones != 1 || probes != 1 {
				t.Fatalf("update=%+v clones=%d probes=%d", update, clones, probes)
			}
		})
	}
}

func TestCanonicalOperationFailureRecoversOnlyAfterConfirmedCorruption(t *testing.T) {
	for _, stage := range []string{"object-inspection", "ancestry", "incremental-scan", "full-scan"} {
		for _, integrity := range []string{"corrupt", "healthy", "inconclusive"} {
			t.Run(stage+"/"+integrity, func(t *testing.T) {
				root := t.TempDir()
				canonical := filepath.Join(root, "core.git")
				if err := os.MkdirAll(canonical, 0700); err != nil {
					t.Fatal(err)
				}
				oldOID := strings.Repeat("a", 40)
				newOID := strings.Repeat("b", 40)
				previous := RepoState{}
				if stage != "full-scan" {
					previous = RepoState{Repo: RepoCore, RemoteURL: CoreRemote, BranchRef: "refs/heads/main", TipOID: oldOID, AlgorithmVersion: AlgorithmVersion, Complete: true, Events: map[string]time.Time{"old": time.Unix(1, 0)}}
				}
				clones, probes := 0, 0
				operationErr := &CommandError{Args: []string{stage}, Status: 128, Err: errors.New(stage + " failed"), Output: "bad object"}
				runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
					op := gitOperation(args)
					joined := strings.Join(args, " ")
					canonicalCall := strings.Contains(joined, canonical)
					switch op {
					case "ls-remote":
						return []byte("ref: refs/heads/main\tHEAD\n"), nil
					case "for-each-ref":
						return nil, nil
					case "cat-file":
						if stage == "object-inspection" && canonicalCall {
							return nil, operationErr
						}
						if strings.Contains(joined, " -t ") {
							return []byte("commit\n"), nil
						}
						return nil, nil
					case "fetch", "update-ref":
						return nil, nil
					case "rev-parse":
						if strings.Contains(joined, "--is-shallow-repository") {
							return []byte("false\n"), nil
						}
						return []byte(newOID + "\n"), nil
					case "merge-base":
						if stage == "ancestry" && canonicalCall {
							return nil, operationErr
						}
						return nil, nil
					case "fsck":
						probes++
						switch integrity {
						case "corrupt":
							return nil, &CommandError{Args: args, Status: 2, Err: errors.New("connectivity"), Output: "broken link from commit to missing tree"}
						case "inconclusive":
							return nil, &CommandError{Args: args, Status: 2, Err: errors.New("probe failed"), Output: "unable to read object: permission denied while inspecting object store"}
						default:
							return nil, nil
						}
					case "clone":
						clones++
						return nil, os.MkdirAll(args[len(args)-1], 0700)
					case "config":
						return []byte(CoreRemote + "\n"), nil
					case "symbolic-ref":
						return []byte("refs/heads/main\n"), nil
					default:
						return nil, fmt.Errorf("unexpected git invocation %v", args)
					}
				})
				cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(_ context.Context, dir string, _ Revision, _ RepoKind) (map[string]time.Time, error) {
					if filepath.Base(dir) == "core.git" && (stage == "incremental-scan" || stage == "full-scan") {
						return nil, operationErr
					}
					return map[string]time.Time{"recovered": time.Unix(1, 0)}, nil
				})}
				update, err := cache.prepareRepo(context.Background(), RepoCore, previous, nil, false)
				if integrity == "corrupt" {
					if err != nil || !update.RecoveredRepository || clones != 1 || probes != 1 {
						t.Fatalf("update=%+v err=%v clones=%d probes=%d", update, err, clones, probes)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), stage+" failed") || clones != 0 || probes != 1 {
					t.Fatalf("err=%v clones=%d probes=%d", err, clones, probes)
				}
				if _, statErr := os.Stat(canonical); statErr != nil {
					t.Fatalf("healthy or inconclusive repository was removed: %v", statErr)
				}
			})
		}
	}
}

func TestPendingFullScanFailurePreservesCheckpointWithoutConfirmedCorruption(t *testing.T) {
	for _, integrity := range []string{"healthy", "inconclusive"} {
		t.Run(integrity, func(t *testing.T) {
			root := t.TempDir()
			pending := filepath.Join(root, "core.git.bootstrap")
			if err := os.MkdirAll(pending, 0700); err != nil {
				t.Fatal(err)
			}
			clones, probes := 0, 0
			runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch gitOperation(args) {
				case "ls-remote":
					return []byte("ref: refs/heads/main\tHEAD\n"), nil
				case "config":
					return []byte(CoreRemote + "\n"), nil
				case "symbolic-ref":
					return []byte("refs/heads/main\n"), nil
				case "rev-parse":
					if strings.Contains(strings.Join(args, " "), "--is-shallow-repository") {
						return []byte("false\n"), nil
					}
					return []byte(strings.Repeat("a", 40) + "\n"), nil
				case "fetch", "update-ref":
					return nil, nil
				case "fsck":
					probes++
					if integrity == "healthy" {
						return nil, nil
					}
					return nil, &CommandError{Args: args, Status: 2, Err: errors.New("probe failed"), Output: "resource temporarily unavailable"}
				case "clone":
					clones++
					return nil, nil
				default:
					return nil, fmt.Errorf("unexpected git invocation %v", args)
				}
			})
			cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
				return nil, &CommandError{Args: []string{"log"}, Status: 128, Err: errors.New("bad object"), Output: "bad object"}
			})}
			if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "bad object") {
				t.Fatal(err)
			}
			if clones != 0 || probes != 1 {
				t.Fatalf("clones=%d probes=%d", clones, probes)
			}
			if _, err := os.Stat(pending); err != nil {
				t.Fatalf("checkpoint was removed: %v", err)
			}
		})
	}
}

func TestPendingFetchTransientAndLaunchFailuresPreserveCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"network", &CommandError{Status: 128, Err: errors.New("connection timed out"), Output: "connection timed out"}},
		{"authentication", &CommandError{Status: 128, Err: errors.New("authentication failed"), Output: "authentication failed"}},
		{"permission", &CommandError{Status: 128, Err: errors.New("permission denied"), Output: "permission denied"}},
		{"resource", &CommandError{Status: 128, Err: errors.New("resource temporarily unavailable"), Output: "resource temporarily unavailable"}},
		{"launch", &CommandError{Status: -1, Err: errors.New("executable not found")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pending := filepath.Join(root, "core.git.bootstrap")
			if err := os.MkdirAll(pending, 0700); err != nil {
				t.Fatal(err)
			}
			clones, probes := 0, 0
			runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch gitOperation(args) {
				case "config":
					return []byte(CoreRemote + "\n"), nil
				case "symbolic-ref":
					return []byte("refs/heads/main\n"), nil
				case "rev-parse":
					if strings.Contains(strings.Join(args, " "), "--is-shallow-repository") {
						return []byte("false\n"), nil
					}
					return []byte(strings.Repeat("a", 40) + "\n"), nil
				case "fetch":
					return nil, tc.err
				case "fsck":
					probes++
					return nil, nil
				case "clone":
					clones++
					return nil, nil
				default:
					return nil, fmt.Errorf("unexpected git invocation %v", args)
				}
			})
			cache := Cache{Root: root, Git: runner}
			if _, _, _, err := cache.prepareBootstrapRepository(context.Background(), CoreRemote, "refs/heads/main", RepoCore, nil); err == nil {
				t.Fatal("expected fetch failure")
			}
			if clones != 0 || probes != 0 {
				t.Fatalf("clones=%d probes=%d", clones, probes)
			}
			if _, err := os.Stat(pending); err != nil {
				t.Fatalf("checkpoint was removed: %v", err)
			}
		})
	}
}

func TestHealthyConnectivityAfterFetchFailurePreservesCheckpoint(t *testing.T) {
	root := t.TempDir()
	pending := filepath.Join(root, "core.git.bootstrap")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	clones, probes := 0, 0
	runner := gitRunnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch gitOperation(args) {
		case "config":
			return []byte(CoreRemote + "\n"), nil
		case "symbolic-ref":
			return []byte("refs/heads/main\n"), nil
		case "rev-parse":
			if strings.Contains(strings.Join(args, " "), "--is-shallow-repository") {
				return []byte("false\n"), nil
			}
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case "fetch":
			return nil, &CommandError{Status: 128, Err: errors.New("remote disconnected"), Output: "remote disconnected"}
		case "fsck":
			probes++
			return nil, nil
		case "clone":
			clones++
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected git invocation %v", args)
		}
	})
	cache := Cache{Root: root, Git: runner}
	if _, _, _, err := cache.prepareBootstrapRepository(context.Background(), CoreRemote, "refs/heads/main", RepoCore, nil); err == nil {
		t.Fatal("expected fetch failure")
	}
	if clones != 0 || probes != 1 {
		t.Fatalf("clones=%d probes=%d", clones, probes)
	}
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("checkpoint was removed: %v", err)
	}
}

func TestStillShallowWithHealthyConnectivityDoesNotRecoveryClone(t *testing.T) {
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
		if gitOperation(args) == "fetch" {
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
		if len(args) > 3 && args[2] == "config" {
			return []byte(CoreRemote + "\n"), nil
		}
		if len(args) > 3 && args[2] == "symbolic-ref" {
			return []byte("refs/heads/main\n"), nil
		}
		if len(args) > 2 && args[2] == "update-ref" {
			return nil, nil
		}
		if len(args) > 2 && args[2] == "fsck" {
			return nil, nil
		}
		return nil, errors.New("unexpected git invocation")
	})
	cache := Cache{Root: root, Git: runner, Scanner: scannerFunc(func(context.Context, string, Revision, RepoKind) (map[string]time.Time, error) {
		return map[string]time.Time{"must-not-scan": time.Unix(1, 0)}, nil
	})}
	if _, err := cache.prepareRepo(context.Background(), RepoCore, RepoState{}, nil, false); err == nil || !strings.Contains(err.Error(), "remains shallow") {
		t.Fatal(err)
	}
	if clones != 0 {
		t.Fatalf("healthy repository triggered %d recovery clones", clones)
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
		if gitOperation(args) == "fetch" {
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

func TestExistingCacheFetchPinsValidatedOriginURL(t *testing.T) {
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
		if gitOperation(args) == "fetch" {
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
	if !strings.Contains(joined, "remote.origin.url="+CoreRemote) || !strings.Contains(joined, " origin ") {
		t.Fatalf("fetch did not pin the validated origin URL: %q", joined)
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
		if len(args) > 3 && args[2] == "config" {
			return []byte(CoreRemote + "\n"), nil
		}
		if len(args) > 3 && args[2] == "symbolic-ref" {
			return []byte("refs/heads/main\n"), nil
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
		if calls == 3 {
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
		if calls >= 3 {
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
		if gitOperation(args) == "fetch" {
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
		if gitOperation(args) == "update-ref" || gitOperation(args) == "fetch" {
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
		if len(args) > 3 && args[2] == "config" {
			return []byte(CoreRemote + "\n"), nil
		}
		if len(args) > 3 && args[2] == "symbolic-ref" {
			return []byte("refs/heads/main\n"), nil
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
