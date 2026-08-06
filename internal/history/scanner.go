package history

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/milo/brewnicle/internal/domain"
)

func LogArgs(gitDir, ref string, repo RepoKind) []string {
	root := "Formula"
	if repo == RepoCask {
		root = "Casks"
	}
	return []string{"--git-dir", gitDir, "log", ref, "--no-renames", "--diff-filter=A", "--format=%x1e%ct", "--name-only", "-z", "--", root}
}

type Scanner struct{ Git string }

func (s Scanner) Scan(ctx context.Context, gitDir, ref string, repo RepoKind) (map[string]time.Time, error) {
	git := s.Git
	if git == "" {
		git = "git"
	}
	cmd := exec.CommandContext(ctx, git, LogArgs(gitDir, ref, repo)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8192}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	events, parseErr := ParseLog(stdout, repo)
	if parseErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, parseErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("git log: %w: %s", waitErr, stderr.String())
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
		if !earliest.IsZero() {
			u := earliest.UTC()
			p.AddedAt = &u
		}
		p.UpdatedAt = updated.UTC()
	}
	return out
}
