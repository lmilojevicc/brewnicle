package history

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	CoreRemote   = "https://github.com/Homebrew/homebrew-core.git"
	CaskRemote   = "https://github.com/Homebrew/homebrew-cask.git"
	LastGoodRef  = "refs/brewnicle/last-good"
	CandidateRef = "refs/brewnicle/candidate"
)

var branchRE = regexp.MustCompile(`^refs/heads/[A-Za-z0-9._/-]+$`)

type ProgressFunc func(string)

type EventScanner interface {
	Scan(context.Context, string, string, RepoKind) (map[string]time.Time, error)
}

type Cache struct {
	// Boundary is a trusted, existing directory that contains Root. When empty,
	// Root's parent is used. Every component below Boundary is checked without
	// following symlinks before Git is allowed to run.
	Boundary string
	Root     string
	Git      GitRunner
	Scanner  EventScanner
}

func (c *Cache) UpdateAll(ctx context.Context, progress ProgressFunc) (map[string]time.Time, map[string]time.Time, error) {
	core, err := c.update(ctx, "core.git", CoreRemote, RepoCore, progress)
	if err != nil {
		return nil, nil, err
	}
	casks, err := c.update(ctx, "cask.git", CaskRemote, RepoCask, progress)
	if err != nil {
		return nil, nil, err
	}
	return core, casks, nil
}

func (c *Cache) update(ctx context.Context, name, remote string, kind RepoKind, progress ProgressFunc) (map[string]time.Time, error) {
	if c.Git == nil {
		c.Git = ExecGit{}
	}
	if c.Scanner == nil {
		c.Scanner = Scanner{}
	}
	repo, exists, err := c.safeRepository(name)
	if err != nil {
		return nil, err
	}
	if progress != nil {
		progress(string(kind) + ": discovering branch")
	}
	out, err := c.Git.Run(ctx, "", "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return nil, err
	}
	branch, err := parseDefaultBranch(string(out))
	if err != nil {
		return nil, err
	}
	if !exists {
		if progress != nil {
			progress(string(kind) + ": cloning (blob filter requested)")
		}
		return c.clone(ctx, repo, remote, branch, kind, true)
	}
	if progress != nil {
		progress(string(kind) + ": fetching (blob filter requested)")
	}
	// Fetch from the authoritative URL directly. The cached repository's origin
	// is mutable local state and is never trusted as an upstream identity.
	args := []string{"--git-dir", repo, "fetch", "--filter=blob:none", remote, "+" + branch + ":" + CandidateRef}
	o, fetchErr := c.Git.Run(ctx, "", args...)
	if fetchErr != nil && unsupportedFilter(string(o)+fetchErr.Error()) {
		_, fetchErr = c.Git.Run(ctx, "", "--git-dir", repo, "fetch", remote, "+"+branch+":"+CandidateRef)
	}
	if fetchErr != nil {
		return nil, fetchErr
	}
	events, err := c.Scanner.Scan(ctx, repo, CandidateRef, kind)
	if err != nil {
		return nil, err
	}
	if _, err = c.Git.Run(ctx, "", "--git-dir", repo, "update-ref", LastGoodRef, CandidateRef); err != nil {
		return nil, err
	}
	return events, nil
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

// secureDirectoryWithin creates target one component at a time beneath a
// trusted real directory. It never follows a symlink in the app-controlled
// portion of the path.
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

// validateRepositoryWritePaths checks only the bounded set of paths Git may
// mutate during fetch/update-ref. It intentionally does not recursively walk
// the potentially huge object database.
func validateRepositoryWritePaths(repo string) error {
	for _, name := range []string{"config", "HEAD", "packed-refs", "FETCH_HEAD"} {
		if err := rejectSymlink(repo, name, false); err != nil {
			return err
		}
	}
	for _, name := range []string{
		"objects",
		filepath.Join("objects", "pack"),
		filepath.Join("objects", "info"),
		filepath.Join("objects", "info", "commit-graphs"),
		"refs",
		filepath.Join("refs", "brewnicle"),
	} {
		if err := rejectSymlink(repo, name, true); err != nil {
			return err
		}
	}
	for _, name := range []string{
		filepath.Join("objects", "info", "commit-graph"),
		filepath.Join("objects", "info", "commit-graph.lock"),
	} {
		if err := rejectSymlink(repo, name, false); err != nil {
			return err
		}
	}
	for _, ref := range []string{CandidateRef, LastGoodRef} {
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
			info, statErr := os.Lstat(filepath.Join(objects, entry.Name()))
			if statErr != nil {
				return statErr
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("repository write path %q must not be a symlink", filepath.Join("objects", entry.Name()))
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// With fetch.writeCommitGraph enabled, Git can update either the monolithic
	// objects/info/commit-graph file or files immediately below the split
	// objects/info/commit-graphs directory. Inspect that small bounded directory
	// so a reused cache cannot redirect those writes through nested symlinks.
	commitGraphs := filepath.Join(repo, "objects", "info", "commit-graphs")
	entries, err = os.ReadDir(commitGraphs)
	if err == nil {
		for _, entry := range entries {
			relative := filepath.Join("objects", "info", "commit-graphs", entry.Name())
			info, statErr := os.Lstat(filepath.Join(commitGraphs, entry.Name()))
			if statErr != nil {
				return statErr
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

func (c *Cache) clone(ctx context.Context, repo, remote, branch string, kind RepoKind, filter bool) (map[string]time.Time, error) {
	parent := filepath.Dir(repo)
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(repo)+"-tmp-")
	if err != nil {
		return nil, err
	}
	if err = os.Remove(tmp); err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	args := []string{"clone", "--bare", "--single-branch", "--branch", strings.TrimPrefix(branch, "refs/heads/")}
	if filter {
		args = append(args, "--filter=blob:none")
	}
	args = append(args, remote, tmp)
	out, err := c.Git.Run(ctx, "", args...)
	if err != nil && filter && unsupportedFilter(string(out)+err.Error()) {
		return c.clone(ctx, repo, remote, branch, kind, false)
	}
	if err != nil {
		return nil, err
	}
	// Validate the candidate history before assigning the last-good ref or
	// publishing the repository at its canonical cache path.
	events, err := c.Scanner.Scan(ctx, tmp, "HEAD", kind)
	if err != nil {
		return nil, err
	}
	if _, err = c.Git.Run(ctx, "", "--git-dir", tmp, "update-ref", LastGoodRef, "HEAD"); err != nil {
		return nil, err
	}
	if err = os.Rename(tmp, repo); err != nil {
		return nil, err
	}
	return events, nil
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
