package history

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

type GitRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type ExecGit struct{ Binary string }

func (g ExecGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	bin := g.Binary
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8192}
	if err := cmd.Run(); err != nil {
		combined := append(append([]byte(nil), stdout.Bytes()...), stderr.Bytes()...)
		return combined, fmt.Errorf("git %s: %w: %s", args[0], err, string(combined))
	}
	return stdout.Bytes(), nil
}
