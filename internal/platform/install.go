package platform

import (
	"context"
	"fmt"
	"github.com/lmilojevicc/brewnicle/internal/catalog"
	"github.com/lmilojevicc/brewnicle/internal/domain"
	"os/exec"
)

func BuildInstallCommand(ctx context.Context, p domain.Package, find LookupFunc) (*exec.Cmd, error) {
	target := p.InstallTarget
	if target == "" {
		target = p.Name
	}
	if !p.Kind.Valid() || !catalog.ValidIdentifier(target) || target != p.Name {
		return nil, fmt.Errorf("invalid catalog install target")
	}
	brew, err := lookup(find, "brew")
	if err != nil {
		return nil, fmt.Errorf("Homebrew is unavailable: %w", err)
	}
	args := []string{"install"}
	if p.Kind != domain.KindFormula {
		args = append(args, "--cask")
	}
	args = append(args, target)
	cmd := exec.CommandContext(ctx, brew, args...)
	return cmd, nil
}
func InstallDisplay(p domain.Package) string {
	target := p.InstallTarget
	if target == "" {
		target = p.Name
	}
	if p.Kind == domain.KindFormula {
		return "brew install " + target
	}
	return "brew install --cask " + target
}
