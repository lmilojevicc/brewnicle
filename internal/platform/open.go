package platform

import (
	"context"
	"fmt"
	"github.com/lmilojevicc/brewnicle/internal/domain"
	"net/url"
	"os/exec"
	"runtime"
)

func BuildOpenCommand(ctx context.Context, p domain.Package, goos string, find LookupFunc) (*exec.Cmd, error) {
	u, err := url.Parse(p.Homepage)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("homepage is unavailable")
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	name := ""
	switch goos {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return nil, fmt.Errorf("homepage opening is unsupported on %s", goos)
	}
	bin, err := lookup(find, name)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, bin, u.String()), nil
}
func Open(ctx context.Context, p domain.Package, goos string, find LookupFunc, runner Runner) error {
	cmd, err := BuildOpenCommand(ctx, p, goos, find)
	if err != nil {
		return err
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return runner.Run(ctx, cmd)
}
