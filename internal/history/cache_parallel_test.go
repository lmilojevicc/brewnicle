package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// bootstrapGit supplies only the commands needed to exercise the first-run
// cache path; no Git process or network request is made.
func bootstrapGit(_ context.Context, _ string, args ...string) ([]byte, error) {
	switch gitOperation(args) {
	case "ls-remote":
		return []byte("ref: refs/heads/main\tHEAD\n"), nil
	case "clone":
		return nil, os.MkdirAll(args[len(args)-1], 0700)
	case "rev-parse":
		if len(args) > 3 && args[3] == "--is-shallow-repository" {
			return []byte("false\n"), nil
		}
		if len(args) > 3 && args[3] == "--verify" {
			return []byte(strings.Repeat(bootstrapOID(args[1]), 40) + "\n"), nil
		}
	case "config":
		return []byte(RemoteFor(bootstrapKind(args[1])) + "\n"), nil
	case "symbolic-ref":
		return []byte("refs/heads/main\n"), nil
	case "update-ref":
		return nil, nil
	case "fsck":
		// An ordinary scanner failure is not evidence of corrupt Git objects.
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected Git command: %v", args)
}

func bootstrapKind(repo string) RepoKind {
	if strings.Contains(repo, "core.git") {
		return RepoCore
	}
	return RepoCask
}

func bootstrapOID(repo string) string {
	if bootstrapKind(repo) == RepoCore {
		return "a"
	}
	return "b"
}

func bootstrapEvents(kind RepoKind) map[string]time.Time {
	return map[string]time.Time{string(kind) + "pkg": time.Unix(100, 0).UTC()}
}

func awaitKind(t *testing.T, started <-chan RepoKind) RepoKind {
	t.Helper()
	select {
	case kind := <-started:
		return kind
	case <-time.After(5 * time.Second):
		t.Fatal("both scans did not start before either was released")
		return ""
	}
}

func TestPrepareAllOverlapsFirstRunScansAndKeepsRepositoryResults(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-cache")
	started := make(chan RepoKind, 2)
	release := make(chan struct{})
	cache := Cache{Root: root, Git: gitRunnerFunc(bootstrapGit), Scanner: scannerFunc(func(ctx context.Context, dir string, rev Revision, kind RepoKind) (map[string]time.Time, error) {
		if bootstrapKind(dir) != kind || rev.FromOID != "" || rev.ToOID != strings.Repeat(bootstrapOID(dir), 40) {
			return nil, fmt.Errorf("wrong scan inputs: %s %+v %s", dir, rev, kind)
		}
		started <- kind
		select {
		case <-release:
			return bootstrapEvents(kind), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})}
	type outcome struct {
		prepared Prepared
		err      error
	}
	result := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var progress []string // deliberately not concurrency-safe: PrepareAll must serialize callbacks
	go func() {
		prepared, err := cache.PrepareAll(ctx, State{}, func(detail string) { progress = append(progress, detail) })
		result <- outcome{prepared, err}
	}()
	first, second := awaitKind(t, started), awaitKind(t, started)
	if first == second {
		t.Fatalf("same repository started twice: %s", first)
	}
	select {
	case got := <-result:
		t.Fatalf("returned before both scans finished: %+v", got)
	default:
	}
	close(release)
	got := <-result
	if got.err != nil || !got.prepared.State.CompleteCurrent() {
		t.Fatalf("incomplete preparation: %+v, %v", got.prepared, got.err)
	}
	if len(progress) < 2 {
		t.Fatalf("missing repository progress: %v", progress)
	}
	for i, kind := range [...]RepoKind{RepoCore, RepoCask} {
		update := got.prepared.Updates[i]
		if update.State.Repo != kind || update.State.TipOID != strings.Repeat(bootstrapOID(repoName(kind)), 40) || update.Mode != ScanFull || len(update.State.Events) != 1 || len(got.prepared.State.Repo(kind).Events) != 1 {
			t.Fatalf("incorrect %s result at slot %d: %+v", kind, i, update)
		}
		if _, err := os.Stat(filepath.Join(root, repoName(kind))); err != nil {
			t.Fatalf("%s was not promoted: %v", kind, err)
		}
	}
}

func TestPrepareAllCompletedSiblingIsNotPublishedOnFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-cache")
	corePromoted := make(chan struct{})
	scanFailure := errors.New("cask scan failed")
	cache := Cache{
		Root: root,
		Git:  gitRunnerFunc(bootstrapGit),
		Scanner: scannerFunc(func(ctx context.Context, _ string, _ Revision, kind RepoKind) (map[string]time.Time, error) {
			if kind == RepoCask {
				select {
				case <-corePromoted:
					return nil, scanFailure
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return bootstrapEvents(kind), nil
		}),
		ops: &cacheOps{rename: func(from, to string) error {
			if err := os.Rename(from, to); err != nil {
				return err
			}
			if filepath.Base(to) == repoName(RepoCore) && filepath.Base(from) == bootstrapRepoName(RepoCore) {
				close(corePromoted)
			}
			return nil
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prepared, err := cache.PrepareAll(ctx, State{}, nil)
	if !errors.Is(err, scanFailure) || !reflect.DeepEqual(prepared, Prepared{}) {
		t.Fatalf("published partial preparation: %+v, %v", prepared, err)
	}
	if _, err := os.Stat(filepath.Join(root, repoName(RepoCore))); err != nil {
		t.Fatalf("successful sibling's cache was not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, repoName(RepoCask))); !os.IsNotExist(err) {
		t.Fatalf("failed repository was promoted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, bootstrapRepoName(RepoCask))); err != nil {
		t.Fatalf("failed repository's download was not checkpointed: %v", err)
	}
}

// This is a hermetic scan-latency comparison, not an end-to-end Git or
// network benchmark. Each repository scan contributes the same fixed delay.
func BenchmarkFirstRunPreparation(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		name := "sequential"
		if parallel {
			name = "parallel"
		}
		b.Run(name, func(b *testing.B) {
			base := b.TempDir()
			for i := 0; i < b.N; i++ {
				cache := Cache{Root: filepath.Join(base, fmt.Sprintf("cache-%d", i)), Git: gitRunnerFunc(bootstrapGit), Scanner: scannerFunc(func(ctx context.Context, _ string, _ Revision, kind RepoKind) (map[string]time.Time, error) {
					select {
					case <-time.After(20 * time.Millisecond):
						return bootstrapEvents(kind), nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				})}
				if parallel {
					if _, err := cache.PrepareAll(context.Background(), State{}, nil); err != nil {
						b.Fatal(err)
					}
				} else {
					for _, kind := range [...]RepoKind{RepoCore, RepoCask} {
						if _, err := cache.prepareRepo(context.Background(), kind, RepoState{}, nil, false); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}

func TestPrepareAllFailureCancelsAndWaitsForOtherScan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-cache")
	caskStarted := make(chan struct{})
	caskCanceled := make(chan struct{})
	allowCaskExit := make(chan struct{})
	scanFailure := errors.New("core scan failed")
	cache := Cache{Root: root, Git: gitRunnerFunc(bootstrapGit), Scanner: scannerFunc(func(ctx context.Context, _ string, _ Revision, kind RepoKind) (map[string]time.Time, error) {
		if kind == RepoCore {
			select {
			case <-caskStarted:
				return nil, scanFailure
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		close(caskStarted)
		<-ctx.Done()
		close(caskCanceled)
		<-allowCaskExit
		return nil, ctx.Err()
	})}
	type outcome struct {
		prepared Prepared
		err      error
	}
	result := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		prepared, err := cache.PrepareAll(ctx, State{}, nil)
		result <- outcome{prepared, err}
	}()
	select {
	case <-caskCanceled:
	case <-ctx.Done():
		t.Fatal("sibling scan was not canceled after failure")
	}
	select {
	case got := <-result:
		t.Fatalf("returned before canceled scan stopped: %+v", got)
	default:
	}
	close(allowCaskExit)
	got := <-result
	if !errors.Is(got.err, scanFailure) || !reflect.DeepEqual(got.prepared, Prepared{}) {
		t.Fatalf("expected original failure and no prepared result: %+v", got)
	}
	for _, kind := range [...]RepoKind{RepoCore, RepoCask} {
		if _, err := os.Stat(filepath.Join(root, repoName(kind))); !os.IsNotExist(err) {
			t.Fatalf("%s promoted after failure: %v", kind, err)
		}
		if _, err := os.Stat(filepath.Join(root, bootstrapRepoName(kind))); err != nil {
			t.Fatalf("%s checkpoint lost after failure: %v", kind, err)
		}
	}
}
