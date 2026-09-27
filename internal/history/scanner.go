package history

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/lmilojevicc/brewnicle/internal/domain"
)

type Revision struct {
	FromOID string
	ToOID   string
}

func (r Revision) String() (string, error) {
	if !ValidOID(r.ToOID) {
		return "", fmt.Errorf("invalid target OID")
	}
	if r.FromOID == "" {
		return r.ToOID, nil
	}
	if !ValidOID(r.FromOID) {
		return "", fmt.Errorf("invalid base OID")
	}
	return r.FromOID + ".." + r.ToOID, nil
}

func LogArgs(gitDir string, rev Revision, repo RepoKind) ([]string, error) {
	root := "Formula"
	if repo == RepoCask {
		root = "Casks"
	} else if repo != RepoCore {
		return nil, fmt.Errorf("invalid repository kind %q", repo)
	}
	revision, err := rev.String()
	if err != nil {
		return nil, err
	}
	return []string{"--git-dir", gitDir, "log", "--full-history", "-c", "--no-renames", "--diff-filter=A", "--format=%x1e%ct", "--name-only", "-z", revision, "--", root}, nil
}

type Scanner struct{ Git string }

func (s Scanner) Scan(ctx context.Context, gitDir string, rev Revision, repo RepoKind) (map[string]time.Time, error) {
	args, err := LogArgs(gitDir, rev, repo)
	if err != nil {
		return nil, err
	}
	git := s.Git
	if git == "" {
		git = "git"
	}
	runArgs := append([]string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always"}, args...)
	cmd := exec.CommandContext(ctx, git, runArgs...)
	cmd.Env = hardenedGitEnvironment(os.Environ())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nonRecoverableError{fmt.Errorf("git log stdout: %w", err)}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8192}
	if err = cmd.Start(); err != nil {
		return nil, nonRecoverableError{newCommandError(runArgs, nil, err)}
	}
	events, parseErr := ParseLog(stdout, repo)
	if parseErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, nonRecoverableError{parseErr}
	}
	if waitErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, newCommandError(runArgs, stderr.Bytes(), waitErr)
	}
	if rev.FromOID == "" && len(events) == 0 {
		return nil, fmt.Errorf("full history scan contained no valid package events")
	}
	return events, nil
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	orig := len(p)
	if l.n <= 0 {
		return orig, nil
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	_, e := l.w.Write(p)
	l.n -= len(p)
	return orig, e
}

func Resolve(packages []domain.Package, core, casks map[string]time.Time, updated time.Time) []domain.Package {
	out := make([]domain.Package, len(packages))
	copy(out, packages)
	for i := range out {
		p := &out[i]
		events := core
		if p.Kind != domain.KindFormula {
			events = casks
		}
		names := append([]string{p.Name}, p.FormerNames...)
		var earliest time.Time
		for _, n := range names {
			if t, ok := events[n]; ok && (earliest.IsZero() || t.Before(earliest)) {
				earliest = t
			}
		}
		p.AddedAt = nil
		if !earliest.IsZero() {
			u := earliest.UTC()
			p.AddedAt = &u
		}
		p.UpdatedAt = updated.UTC()
	}
	return out
}
