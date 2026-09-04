package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	CoreRemote   = "https://github.com/Homebrew/homebrew-core.git"
	CaskRemote   = "https://github.com/Homebrew/homebrew-cask.git"
	LastGoodRef  = "refs/brewnicle/last-good" // compatibility pin only
	CandidateRef = "refs/brewnicle/candidate" // compatibility pin only
)

var branchRE = regexp.MustCompile(`^refs/heads/[A-Za-z0-9._/-]+$`)

type ProgressFunc func(string)

type EventScanner interface {
	Scan(context.Context, string, Revision, RepoKind) (map[string]time.Time, error)
}

type cacheOps struct {
	rename    func(string, string) error
	removeAll func(string) error
}

type Cache struct {
	Boundary string
	Root     string
	Git      GitRunner
	Scanner  EventScanner
	ops      *cacheOps
	remotes  map[RepoKind]string // tests only; production always uses compiled URLs
}

func (c *Cache) PrepareAll(ctx context.Context, previous State, progress ProgressFunc) (Prepared, error) {
	var prepared Prepared
	state := CloneState(previous)
	for i, repoKind := range []RepoKind{RepoCore, RepoCask} {
		update, err := c.prepareRepo(ctx, repoKind, state.Repo(repoKind), progress, false)
		if err != nil {
			return Prepared{}, err
		}
		prepared.Updates[i] = update
		state.SetRepo(repoKind, update.State)
	}
	prepared.State = state
	if !prepared.State.CompleteCurrent() && c.remotes == nil {
		return Prepared{}, fmt.Errorf("prepared history state is incomplete")
	}
	return prepared, nil
}

func (c *Cache) ReconcilePublished(ctx context.Context, prepared Prepared) error {
	var errs []error
	for _, update := range prepared.Updates {
		repoPath, exists, err := c.safeRepository(repoName(update.State.Repo))
		if err != nil || !exists {
			if err == nil {
				err = fmt.Errorf("history repository missing")
			}
			errs = append(errs, fmt.Errorf("%s published ref: %w", update.State.Repo, err))
			continue
		}
		expected := update.PublishedExpectation.ExpectedOID
		if expected == "" {
			expected = ZeroOIDFor(update.State.TipOID)
		}
		if expected == "" {
			errs = append(errs, fmt.Errorf("%s published ref: invalid hash width", update.State.Repo))
			continue
		}
		if err = c.validateRepositoryConfig(ctx, repoPath, update.State.RemoteURL); err != nil {
			errs = append(errs, fmt.Errorf("%s published ref: unsafe repository config: %w", update.State.Repo, err))
			continue
		}
		_, err = c.git().Run(ctx, "", "--git-dir", repoPath, "update-ref", PublishedRef, update.State.TipOID, expected)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s published ref: %w", update.State.Repo, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Cache) prepareRepo(ctx context.Context, kind RepoKind, previous RepoState, progress ProgressFunc, recovering bool) (RepoUpdate, error) {
	remote := c.remote(kind)
	name := repoName(kind)
	if err := c.repairRecoveryBackups(name); err != nil {
		return RepoUpdate{}, err
	}
	repo, exists, err := c.safeRepository(name)
	if err != nil {
		return RepoUpdate{}, err
	}
	if progress != nil {
		progress(string(kind) + ": discovering branch")
	}
	out, err := c.git().Run(ctx, "", "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return RepoUpdate{}, err
	}
	branch, err := parseDefaultBranch(string(out))
	if err != nil {
		return RepoUpdate{}, err
	}
	if !exists {
		if progress != nil {
			progress(string(kind) + ": initial clone and full-history setup")
		}
		return c.cloneAndPromote(ctx, repo, remote, branch, kind, progress)
	}
	if err = c.validateRepositoryConfig(ctx, repo, remote); err != nil {
		// Configuration failures are not object-corruption evidence. Do not run
		// fsck through repository-controlled configuration or replace the cache.
		return RepoUpdate{}, fmt.Errorf("unsafe history repository config: %w", err)
	}

	observed, err := c.observeRef(ctx, repo, PublishedRef)
	if err != nil {
		return RepoUpdate{}, err
	}
	expectation := RefExpectation{}
	oldUsable := repoStateUsableFor(previous, kind, remote)
	if oldUsable {
		commit, inspectErr := c.objectIsCommit(ctx, repo, previous.TipOID)
		if inspectErr != nil {
			if !recovering && c.shouldRecoverRepository(ctx, repo, inspectErr) {
				return c.recover(ctx, repo, remote, branch, kind, progress)
			}
			return RepoUpdate{}, inspectErr
		}
		oldUsable = commit
	}
	if oldUsable {
		expected := observed
		if expected == "" {
			expected = ZeroOIDFor(previous.TipOID)
		}
		if _, err = c.git().Run(ctx, "", "--git-dir", repo, "update-ref", PublishedRef, previous.TipOID, expected); err != nil {
			return RepoUpdate{}, err
		}
		expectation.ExpectedOID = previous.TipOID
	} else if observed != "" {
		if _, err = c.git().Run(ctx, "", "--git-dir", repo, "update-ref", "-d", PublishedRef, observed); err != nil {
			return RepoUpdate{}, err
		}
	}

	if progress != nil {
		progress(string(kind) + ": fetching new commits")
	}
	if err = c.fetch(ctx, repo, remote, branch, false); err != nil {
		if !recovering && c.shouldRecoverRepository(ctx, repo, err) {
			return c.recover(ctx, repo, remote, branch, kind, progress)
		}
		return RepoUpdate{}, err
	}
	newOID, err := c.resolveCommit(ctx, repo, FetchedRef)
	if err != nil {
		if !recovering && c.shouldRecoverRepository(ctx, repo, err) {
			return c.recover(ctx, repo, remote, branch, kind, progress)
		}
		return RepoUpdate{}, err
	}

	mode := ScanFull
	base := ""
	events := map[string]time.Time(nil)
	if oldUsable && newOID == previous.TipOID {
		mode = ScanUnchanged
		base = previous.TipOID
		events = CloneEvents(previous.Events)
		if progress != nil {
			progress(string(kind) + ": unchanged; no history scan")
		}
	} else if oldUsable {
		ancestor, ancestorErr := c.isAncestor(ctx, repo, previous.TipOID, newOID)
		if ancestorErr != nil {
			if !recovering && c.shouldRecoverRepository(ctx, repo, ancestorErr) {
				return c.recover(ctx, repo, remote, branch, kind, progress)
			}
			return RepoUpdate{}, ancestorErr
		}
		if ancestor {
			mode = ScanIncremental
			base = previous.TipOID
			if progress != nil {
				progress(string(kind) + ": scanning new commits only")
			}
			delta, scanErr := c.scanner().Scan(ctx, repo, Revision{FromOID: base, ToOID: newOID}, kind)
			if scanErr != nil {
				if !recovering && c.shouldRecoverRepository(ctx, repo, scanErr) {
					return c.recover(ctx, repo, remote, branch, kind, progress)
				}
				return RepoUpdate{}, scanErr
			}
			events = MergeEventsMin(previous.Events, delta)
		}
	}
	if events == nil {
		if progress != nil {
			progress(string(kind) + ": rebuilding complete history")
		}
		if err = c.ensureNonShallow(ctx, repo, remote, branch); err != nil {
			if !recovering && c.shouldRecoverRepository(ctx, repo, err) {
				return c.recover(ctx, repo, remote, branch, kind, progress)
			}
			return RepoUpdate{}, err
		}
		events, err = c.scanner().Scan(ctx, repo, Revision{ToOID: newOID}, kind)
		if err != nil {
			if !recovering && c.shouldRecoverRepository(ctx, repo, err) {
				return c.recover(ctx, repo, remote, branch, kind, progress)
			}
			return RepoUpdate{}, err
		}
	}
	state := RepoState{Repo: kind, RemoteURL: remote, BranchRef: branch, TipOID: newOID, AlgorithmVersion: AlgorithmVersion, Complete: true, Events: events}
	if err = validateCandidateState(state, remote); err != nil {
		return RepoUpdate{}, err
	}
	return RepoUpdate{State: state, Mode: mode, BaseOID: base, PublishedExpectation: expectation, RecoveredRepository: recovering}, nil
}

func (c *Cache) recover(ctx context.Context, canonical, remote, branch string, kind RepoKind, progress ProgressFunc) (RepoUpdate, error) {
	if ctx.Err() != nil {
		return RepoUpdate{}, ctx.Err()
	}
	if progress != nil {
		progress(string(kind) + ": recovering local history cache")
	}
	pending, newOID, events, err := c.scanBootstrapRepository(ctx, remote, branch, kind, progress)
	if err != nil {
		return RepoUpdate{}, err
	}
	state := RepoState{Repo: kind, RemoteURL: remote, BranchRef: branch, TipOID: newOID, AlgorithmVersion: AlgorithmVersion, Complete: true, Events: events}
	if err = validateCandidateState(state, remote); err != nil {
		return RepoUpdate{}, err
	}
	backup := canonical + fmt.Sprintf(".backup-%d", time.Now().UnixNano())
	if err = c.rename()(canonical, backup); err != nil {
		return RepoUpdate{}, err
	}
	if err = c.rename()(pending, canonical); err != nil {
		restoreErr := c.rename()(backup, canonical)
		if restoreErr != nil {
			return RepoUpdate{}, errors.Join(fmt.Errorf("promote recovered repository: %w", err), fmt.Errorf("restore repository from %s: %w", backup, restoreErr))
		}
		return RepoUpdate{}, fmt.Errorf("promote recovered repository (restored from %s): %w", backup, err)
	}
	if err = c.removeAll()(backup); err != nil {
		return RepoUpdate{}, fmt.Errorf("recovered repository promoted but backup cleanup failed at %s: %w", backup, err)
	}
	return RepoUpdate{State: state, Mode: ScanFull, PublishedExpectation: RefExpectation{}, RecoveredRepository: true}, nil
}

func (c *Cache) cloneAndPromote(ctx context.Context, repo, remote, branch string, kind RepoKind, progress ProgressFunc) (RepoUpdate, error) {
	pending, newOID, events, err := c.scanBootstrapRepository(ctx, remote, branch, kind, progress)
	if err != nil {
		return RepoUpdate{}, err
	}
	state := RepoState{Repo: kind, RemoteURL: remote, BranchRef: branch, TipOID: newOID, AlgorithmVersion: AlgorithmVersion, Complete: true, Events: events}
	if err = validateCandidateState(state, remote); err != nil {
		return RepoUpdate{}, err
	}
	if err = c.rename()(pending, repo); err != nil {
		return RepoUpdate{}, err
	}
	return RepoUpdate{State: state, Mode: ScanFull, PublishedExpectation: RefExpectation{}}, nil
}

func (c *Cache) scanBootstrapRepository(ctx context.Context, remote, branch string, kind RepoKind, progress ProgressFunc) (string, string, map[string]time.Time, error) {
	pending, newOID, reused, err := c.prepareBootstrapRepository(ctx, remote, branch, kind, progress)
	if err != nil {
		return "", "", nil, err
	}
	if progress != nil {
		progress(string(kind) + ": scanning complete history")
	}
	events, err := c.scanner().Scan(ctx, pending, Revision{ToOID: newOID}, kind)
	if err != nil && reused && c.shouldRecoverRepository(ctx, pending, err) {
		if removeErr := c.removeCorruptBootstrapRepository(bootstrapRepoName(kind)); removeErr != nil {
			return "", "", nil, errors.Join(err, removeErr)
		}
		pending, newOID, _, err = c.prepareBootstrapRepository(ctx, remote, branch, kind, progress)
		if err == nil {
			events, err = c.scanner().Scan(ctx, pending, Revision{ToOID: newOID}, kind)
		}
	}
	return pending, newOID, events, err
}

// prepareBootstrapRepository checkpoints a completed, validated clone before
// the expensive scan. The pending repository is data only: it does not imply a
// complete history state or an authoritative published index.
func (c *Cache) prepareBootstrapRepository(ctx context.Context, remote, branch string, kind RepoKind, progress ProgressFunc) (string, string, bool, error) {
	name := bootstrapRepoName(kind)
	pending, exists, err := c.safeRepository(name)
	if err != nil {
		// An unsafe or inaccessible checkpoint is preserved for diagnosis. It is
		// never deleted merely because path validation failed.
		return "", "", false, fmt.Errorf("invalid pending history repository: %w", err)
	}
	if exists {
		_, validateErr := c.validateBootstrapRepository(ctx, pending, remote, branch)
		if validateErr == nil {
			if progress != nil {
				progress(string(kind) + ": reusing completed history download")
			}
			// A checkpoint may outlive the catalog request that created it. Fetch
			// the current branch tip before scanning so reuse cannot silently
			// produce unknown dates for packages added since that attempt.
			if err = c.fetch(ctx, pending, remote, branch, false); err == nil {
				err = c.ensureNonShallow(ctx, pending, remote, branch)
			}
			var oid string
			if err == nil {
				oid, err = c.resolveCommit(ctx, pending, FetchedRef)
			}
			if err == nil {
				return pending, oid, true, nil
			}
			if !c.shouldRecoverRepository(ctx, pending, err) {
				return "", "", false, err
			}
			if removeErr := c.removeCorruptBootstrapRepository(name); removeErr != nil {
				return "", "", false, errors.Join(fmt.Errorf("corrupt pending history repository: %w", err), removeErr)
			}
			exists = false
		} else {
			if !c.shouldRecoverRepository(ctx, pending, validateErr) {
				return "", "", false, validateErr
			}
			if removeErr := c.removeCorruptBootstrapRepository(name); removeErr != nil {
				return "", "", false, errors.Join(fmt.Errorf("corrupt pending history repository: %w", validateErr), removeErr)
			}
			exists = false
		}
	}

	parent := filepath.Dir(pending)
	tmp, err := os.MkdirTemp(parent, "."+repoName(kind)+"-tmp-")
	if err != nil {
		return "", "", false, err
	}
	_ = os.Remove(tmp)
	defer c.removeAll()(tmp)
	if err = c.cloneInto(ctx, tmp, remote, branch, true); err != nil {
		return "", "", false, err
	}
	if err = c.ensureNonShallow(ctx, tmp, remote, branch); err != nil {
		return "", "", false, err
	}
	newOID, err := c.validateBootstrapRepository(ctx, tmp, remote, branch)
	if err != nil {
		return "", "", false, err
	}
	if _, err = c.git().Run(ctx, "", "--git-dir", tmp, "update-ref", FetchedRef, newOID); err != nil {
		return "", "", false, err
	}
	if err = c.rename()(tmp, pending); err != nil {
		return "", "", false, err
	}
	return pending, newOID, false, nil
}

func (c *Cache) validateBootstrapRepository(ctx context.Context, repo, remote, branch string) (string, error) {
	safe, exists, err := c.safeRepository(filepath.Base(repo))
	if err != nil {
		return "", nonRecoverableError{err}
	}
	if !exists || filepath.Clean(safe) != filepath.Clean(repo) {
		return "", nonRecoverableError{fmt.Errorf("pending history repository path is not trusted")}
	}
	repo = safe
	if err = validateOwnedDirectory(repo); err != nil {
		return "", nonRecoverableError{err}
	}
	if err = c.validateRepositoryConfig(ctx, repo, remote); err != nil {
		// fsck must never be launched through repository configuration that has
		// not passed the allowlist and alternate-object checks.
		return "", nonRecoverableError{err}
	}
	if _, configErr := os.Lstat(filepath.Join(repo, "config")); os.IsNotExist(configErr) {
		// Test Git runners use directory-only repositories. Preserve the remote
		// assertion there without weakening validation of real repository config.
		out, remoteErr := c.git().Run(ctx, "", "--git-dir", repo, "config", "--get", "remote.origin.url")
		if remoteErr != nil {
			return "", remoteErr
		}
		if strings.TrimSpace(string(out)) != remote {
			return "", fmt.Errorf("pending history repository has unexpected remote")
		}
	}
	out, err := c.git().Run(ctx, "", "--git-dir", repo, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return "", err
	}
	if string(out) != "false\n" {
		return "", fmt.Errorf("pending history repository is shallow")
	}
	out, err = c.git().Run(ctx, "", "--git-dir", repo, "symbolic-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(out)) != branch {
		return "", fmt.Errorf("pending history repository has unexpected branch")
	}
	return c.resolveCommit(ctx, repo, "HEAD")
}

// removeCorruptBootstrapRepository is called only after shouldRecoverRepository
// has positively identified reachable-object corruption.
func (c *Cache) removeCorruptBootstrapRepository(name string) error {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("unsafe pending repository name %q", name)
	}
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return err
	}
	boundary := c.Boundary
	if boundary == "" {
		boundary = filepath.Dir(root)
	}
	root, err = secureDirectoryWithin(boundary, root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, name)
	if !pathWithin(root, path) {
		return fmt.Errorf("unsafe pending repository path")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return os.Remove(path)
	}
	if err = validateOwnedDirectory(path); err != nil {
		return err
	}
	return c.removeAll()(path)
}

func validateOwnedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("history repository must be a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("history repository is not owned by the current user")
	}
	return nil
}

var safeRepositoryConfigKeys = map[string]bool{
	"core.bare":                        true,
	"core.filemode":                    true,
	"core.ignorecase":                  true,
	"core.logallrefupdates":            true,
	"core.precomposeunicode":           true,
	"core.repositoryformatversion":     true,
	"extensions.objectformat":          true,
	"extensions.partialclone":          true,
	"remote.origin.fetch":              true,
	"remote.origin.partialclonefilter": true,
	"remote.origin.promisor":           true,
	"remote.origin.url":                true,
}

// validateRepositoryConfig rejects repository-controlled Git behavior before
// any cached repository is mutated. Includes are deliberately not followed.
func (c *Cache) validateRepositoryConfig(ctx context.Context, repo, remote string) error {
	for _, relative := range []string{filepath.Join("objects", "info", "alternates"), filepath.Join("objects", "info", "http-alternates")} {
		_, err := os.Lstat(filepath.Join(repo, relative))
		if err == nil {
			return fmt.Errorf("repository alternate object store %q is not allowed", relative)
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	configPath := filepath.Join(repo, "config")
	info, err := os.Lstat(configPath)
	if os.IsNotExist(err) {
		// Injected repository fixtures may omit config. Production clones always
		// create it; mutations remain hook- and transport-hardened regardless.
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("repository config must be a regular file")
	}
	out, err := c.git().Run(ctx, "", "config", "--file", configPath, "--no-includes", "--name-only", "--null", "--list")
	if err != nil {
		return fmt.Errorf("read repository config: %w", err)
	}
	for _, raw := range strings.Split(string(out), "\x00") {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" {
			continue
		}
		if !safeRepositoryConfigKeys[key] {
			return fmt.Errorf("repository config key %q is not allowed", key)
		}
	}
	if remote == "" {
		return nil
	}
	out, err = c.git().Run(ctx, "", "config", "--file", configPath, "--no-includes", "--get-all", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("read repository remote: %w", err)
	}
	values := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(values) != 1 || strings.TrimSpace(values[0]) != remote {
		return fmt.Errorf("history repository has unexpected remote")
	}
	return nil
}

func (c *Cache) cloneInto(ctx context.Context, dst, remote, branch string, filter bool) error {
	args := []string{"clone", "--bare", "--single-branch", "--branch", strings.TrimPrefix(branch, "refs/heads/")}
	if filter {
		args = append(args, "--filter=blob:none")
	}
	args = append(args, remote, dst)
	out, err := c.git().Run(ctx, "", args...)
	if err != nil && filter && unsupportedFilter(string(out)+err.Error()) {
		_ = c.removeAll()(dst)
		return c.cloneInto(ctx, dst, remote, branch, false)
	}
	return err
}

func (c *Cache) fetch(ctx context.Context, repo, remote, branch string, unshallow bool) error {
	// Pin the validated origin URL through command-line configuration and fetch
	// by name. Fetching a URL with a partial-clone filter creates a persistent
	// remote.<url>.promisor section, making the next validation fail.
	args := []string{"--git-dir", repo, "-c", "remote.origin.url=" + remote, "fetch", "--filter=blob:none"}
	if unshallow {
		args = append(args, "--unshallow")
	}
	args = append(args, "origin", "+"+branch+":"+FetchedRef)
	out, err := c.git().Run(ctx, "", args...)
	if err != nil && unsupportedFilter(string(out)+err.Error()) {
		args = []string{"--git-dir", repo, "-c", "remote.origin.url=" + remote, "fetch"}
		if unshallow {
			args = append(args, "--unshallow")
		}
		args = append(args, "origin", "+"+branch+":"+FetchedRef)
		_, err = c.git().Run(ctx, "", args...)
	}
	return err
}

func (c *Cache) ensureNonShallow(ctx context.Context, repo, remote, branch string) error {
	out, err := c.git().Run(ctx, "", "--git-dir", repo, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	switch string(out) {
	case "false\n":
		return nil
	case "true\n":
		if err = c.fetch(ctx, repo, remote, branch, true); err != nil {
			// Network/auth/cancellation are not local corruption and must not trigger clone recovery.
			if ctx.Err() != nil || isNetworkOrAuthError(err) {
				return nonRecoverableError{err}
			}
			return err
		}
		out, err = c.git().Run(ctx, "", "--git-dir", repo, "rev-parse", "--is-shallow-repository")
		if err != nil {
			return err
		}
		if string(out) != "false\n" {
			return fmt.Errorf("repository remains shallow after unshallow")
		}
		return nil
	default:
		return fmt.Errorf("unexpected shallow status %q", out)
	}
}

type nonRecoverableError struct{ error }

func (e nonRecoverableError) Unwrap() error { return e.error }

// shouldRecoverRepository only authorizes deletion after a hardened local fsck
// confirms that reachable repository data is corrupt. Transient remote and
// process-launch failures preserve the existing repository unchanged.
func (c *Cache) shouldRecoverRepository(ctx context.Context, repo string, operationErr error) bool {
	var nonRecoverable nonRecoverableError
	if operationErr == nil || ctx.Err() != nil || errors.Is(operationErr, context.Canceled) || errors.Is(operationErr, context.DeadlineExceeded) || errors.As(operationErr, &nonRecoverable) || commandLaunchFailed(operationErr) || isNetworkOrAuthError(operationErr) || isLocalResourceError(operationErr) {
		return false
	}
	_, probeErr := c.git().Run(ctx, "", "--git-dir", repo, "fsck", "--connectivity-only", "--no-dangling")
	if probeErr == nil || ctx.Err() != nil || commandLaunchFailed(probeErr) {
		return false
	}
	var commandErr *CommandError
	if !errors.As(probeErr, &commandErr) || commandErr.Status <= 0 {
		return false
	}
	message := strings.ToLower(commandErr.Output + " " + commandErr.Error())
	for _, marker := range []string{"missing ", "missing blob", "missing tree", "missing commit", "broken link", "invalid sha1 pointer", "invalid sha256 pointer", "object corrupt", "corrupt loose object", " is empty", "is corrupt", "sha1 mismatch", "sha256 mismatch", "bad sha1 file", "bad sha256 file"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (c *Cache) observeRef(ctx context.Context, repo, ref string) (string, error) {
	out, err := c.git().Run(ctx, "", "--git-dir", repo, "for-each-ref", "--format=%(objectname)", ref)
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(out), "\n")
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, "\n") || !ValidOID(value) {
		return "", fmt.Errorf("invalid ref value")
	}
	return value, nil
}

func (c *Cache) objectIsCommit(ctx context.Context, repo, oid string) (bool, error) {
	_, err := c.git().Run(ctx, "", "--git-dir", repo, "cat-file", "-e", oid)
	if err != nil {
		if status, ok := commandStatus(err); ok && status == 1 {
			return false, nil
		}
		return false, err
	}
	out, err := c.git().Run(ctx, "", "--git-dir", repo, "cat-file", "-t", oid)
	if err != nil {
		return false, err
	}
	return string(out) == "commit\n", nil
}

func (c *Cache) isAncestor(ctx context.Context, repo, oldOID, newOID string) (bool, error) {
	_, err := c.git().Run(ctx, "", "--git-dir", repo, "merge-base", "--is-ancestor", oldOID, newOID)
	if err == nil {
		return true, nil
	}
	if status, ok := commandStatus(err); ok && status == 1 {
		return false, nil
	}
	return false, err
}

func (c *Cache) resolveCommit(ctx context.Context, repo, ref string) (string, error) {
	out, err := c.git().Run(ctx, "", "--git-dir", repo, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSuffix(string(out), "\n")
	if !ValidOID(oid) {
		return "", fmt.Errorf("invalid resolved commit OID")
	}
	return oid, nil
}

func repoStateUsableFor(state RepoState, kind RepoKind, remote string) bool {
	return state.Repo == kind && validateCandidateState(state, remote) == nil
}

func validateCandidateState(state RepoState, remote string) error {
	if err := state.StructurallyValidFor(remote); err != nil {
		return err
	}
	if state.AlgorithmVersion != AlgorithmVersion {
		return fmt.Errorf("unsupported history algorithm for %s", state.Repo)
	}
	return nil
}

func (c *Cache) git() GitRunner {
	if c.Git != nil {
		return c.Git
	}
	return ExecGit{}
}
func (c *Cache) scanner() EventScanner {
	if c.Scanner != nil {
		return c.Scanner
	}
	return Scanner{}
}
func (c *Cache) remote(kind RepoKind) string {
	if c.remotes != nil && c.remotes[kind] != "" {
		return c.remotes[kind]
	}
	return RemoteFor(kind)
}
func (c *Cache) rename() func(string, string) error {
	if c.ops != nil && c.ops.rename != nil {
		return c.ops.rename
	}
	return os.Rename
}
func (c *Cache) removeAll() func(string) error {
	if c.ops != nil && c.ops.removeAll != nil {
		return c.ops.removeAll
	}
	return os.RemoveAll
}
func repoName(kind RepoKind) string {
	if kind == RepoCore {
		return "core.git"
	}
	return "cask.git"
}

func bootstrapRepoName(kind RepoKind) string { return repoName(kind) + ".bootstrap" }

func (c *Cache) repairRecoveryBackups(name string) error {
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return err
	}
	boundary := c.Boundary
	if boundary == "" {
		boundary = filepath.Dir(root)
	}
	root, err = secureDirectoryWithin(boundary, root)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if err = c.cleanupAbandonedCandidates(root, name, entries); err != nil {
		return err
	}

	backupRE := regexp.MustCompile("^" + regexp.QuoteMeta(name) + `\.backup-[0-9]+$`)
	backups := make([]string, 0, 1)
	for _, entry := range entries {
		if !backupRE.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("recovery backup %s is not a real directory", path)
		}
		if err = validateRepositoryWritePaths(path); err != nil {
			return fmt.Errorf("invalid recovery backup %s: %w", path, err)
		}
		backups = append(backups, path)
	}
	canonical := filepath.Join(root, name)
	info, statErr := os.Lstat(canonical)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("cache repository must be a real directory")
		}
		if len(backups) == 0 {
			return nil
		}
		var cleanup []error
		for _, backup := range backups {
			if removeErr := c.removeAll()(backup); removeErr != nil {
				cleanup = append(cleanup, fmt.Errorf("remove stale recovery backup %s: %w", backup, removeErr))
			}
		}
		return errors.Join(cleanup...)
	}
	if !os.IsNotExist(statErr) {
		return statErr
	}
	switch len(backups) {
	case 0:
		return nil
	case 1:
		if err = c.rename()(backups[0], canonical); err != nil {
			return fmt.Errorf("restore recovery backup %s: %w", backups[0], err)
		}
		return nil
	default:
		return fmt.Errorf("ambiguous recovery backups for %s: %d candidates", name, len(backups))
	}
}

func (c *Cache) cleanupAbandonedCandidates(root, name string, entries []os.DirEntry) error {
	candidateRE := regexp.MustCompile(`^\.` + regexp.QuoteMeta(name) + `-(?:tmp|recovery)-[0-9]+$`)
	candidates := make([]string, 0)
	for _, entry := range entries {
		if !candidateRE.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if !pathWithin(root, path) {
			return fmt.Errorf("unsafe abandoned history cache path %s", path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("abandoned history cache %s is not a real directory", path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			return fmt.Errorf("abandoned history cache %s is not owned by the current user", path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if filepath.Clean(resolved) != filepath.Clean(path) || !pathWithin(root, resolved) {
			return fmt.Errorf("abandoned history cache %s escapes the cache root", path)
		}
		if err = validateRepositoryWritePaths(path); err != nil {
			return fmt.Errorf("unsafe abandoned history cache %s: %w", path, err)
		}
		candidates = append(candidates, path)
	}
	var cleanup []error
	for _, candidate := range candidates {
		if err := c.removeAll()(candidate); err != nil {
			cleanup = append(cleanup, fmt.Errorf("remove abandoned history cache %s: %w", candidate, err))
		}
	}
	return errors.Join(cleanup...)
}

func (c *Cache) safeRepository(name string) (string, bool, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return "", false, fmt.Errorf("unsafe cache repository name %q", name)
	}
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return "", false, err
	}
	boundary := c.Boundary
	if boundary == "" {
		boundary = filepath.Dir(root)
	}
	resolvedRoot, err := secureDirectoryWithin(boundary, root)
	if err != nil {
		return "", false, err
	}
	repo := filepath.Join(resolvedRoot, name)
	if !pathWithin(resolvedRoot, repo) {
		return "", false, fmt.Errorf("unsafe cache path")
	}
	info, err := os.Lstat(repo)
	if os.IsNotExist(err) {
		return repo, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, fmt.Errorf("cache repository must be a real directory")
	}
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", false, err
	}
	if !pathWithin(resolvedRoot, resolvedRepo) {
		return "", false, fmt.Errorf("cache repository escapes cache root")
	}
	if err := validateRepositoryWritePaths(resolvedRepo); err != nil {
		return "", false, err
	}
	return resolvedRepo, true, nil
}

func secureDirectoryWithin(boundary, target string) (string, error) {
	boundary, err := filepath.Abs(boundary)
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(boundary)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("cache boundary must be a real directory")
	}
	resolvedBoundary, err := filepath.EvalSymlinks(boundary)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(boundary, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("cache root escapes trusted boundary")
	}
	current := resolvedBoundary
	if rel == "." {
		return current, nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0700); err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("cache path component %q must be a real directory", part)
		}
	}
	return current, nil
}

func validateRepositoryWritePaths(repo string) error {
	for _, name := range []string{"config", "HEAD", "packed-refs", "FETCH_HEAD"} {
		if err := rejectSymlink(repo, name, false); err != nil {
			return err
		}
	}
	for _, name := range []string{"objects", filepath.Join("objects", "pack"), filepath.Join("objects", "info"), filepath.Join("objects", "info", "commit-graphs"), "refs", filepath.Join("refs", "brewnicle")} {
		if err := rejectSymlink(repo, name, true); err != nil {
			return err
		}
	}
	for _, name := range []string{filepath.Join("objects", "info", "commit-graph"), filepath.Join("objects", "info", "commit-graph.lock")} {
		if err := rejectSymlink(repo, name, false); err != nil {
			return err
		}
	}
	for _, ref := range []string{CandidateRef, LastGoodRef, FetchedRef, PublishedRef} {
		for _, suffix := range []string{"", ".lock"} {
			if err := rejectSymlink(repo, filepath.FromSlash(ref+suffix), false); err != nil {
				return err
			}
		}
	}
	objects := filepath.Join(repo, "objects")
	entries, err := os.ReadDir(objects)
	if err == nil {
		for _, entry := range entries {
			info, e := os.Lstat(filepath.Join(objects, entry.Name()))
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("repository write path %q must not be a symlink", filepath.Join("objects", entry.Name()))
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	commitGraphs := filepath.Join(repo, "objects", "info", "commit-graphs")
	entries, err = os.ReadDir(commitGraphs)
	if err == nil {
		for _, entry := range entries {
			relative := filepath.Join("objects", "info", "commit-graphs", entry.Name())
			info, e := os.Lstat(filepath.Join(commitGraphs, entry.Name()))
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("repository write path %q must not be a symlink", relative)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("repository write path %q must be a regular file", relative)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func rejectSymlink(repo, relative string, directory bool) error {
	path := filepath.Join(repo, relative)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("repository write path %q must not be a symlink", relative)
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("repository write path %q must be a directory", relative)
	}
	if !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("repository write path %q must be a regular file", relative)
	}
	return nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func parseDefaultBranch(s string) (string, error) {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "ref: ") && strings.HasSuffix(line, "\tHEAD") {
			r := strings.TrimSuffix(strings.TrimPrefix(line, "ref: "), "\tHEAD")
			if branchRE.MatchString(r) && !strings.Contains(r, "..") {
				return r, nil
			}
		}
	}
	return "", fmt.Errorf("remote HEAD did not name a valid branch")
}
func unsupportedFilter(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "filtering not recognized") || strings.Contains(s, "does not support filter") || strings.Contains(s, "filter-spec") || strings.Contains(s, "filtering is not supported")
}
