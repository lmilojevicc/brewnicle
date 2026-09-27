//go:build integration

package history

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lmilojevicc/brewnicle/internal/catalog"
	"github.com/lmilojevicc/brewnicle/internal/domain"
)

func TestLiveCatalogSourcePathsAreSupported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := catalog.NewClient(nil).Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Packages) < 1000 {
		t.Fatalf("unexpectedly few enabled packages fetched: %d", len(result.Packages))
	}
	for _, pkg := range result.Packages {
		if err := validateCatalogSourcePath(pkg); err != nil {
			t.Error(err)
		}
	}
}

func TestCatalogSourcePathCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		pkg     domain.Package
		wantErr bool
	}{
		{
			name: "font in one-character cask bucket",
			pkg: domain.Package{
				Name:       "font-maple",
				Kind:       domain.KindFont,
				SourcePath: "Casks/f/font-maple.rb",
			},
		},
		{
			name: "font in nested font bucket",
			pkg: domain.Package{
				Name:       "font-maple",
				Kind:       domain.KindFont,
				SourcePath: "Casks/font/font-m/font-maple.rb",
			},
		},
		{
			name: "non-font in nested font bucket",
			pkg: domain.Package{
				Name:       "maple",
				Kind:       domain.KindCask,
				SourcePath: "Casks/font/font-m/maple.rb",
			},
			wantErr: true,
		},
		{
			name: "missing source path",
			pkg: domain.Package{
				Name: "font-maple",
				Kind: domain.KindFont,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCatalogSourcePath(tt.pkg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateCatalogSourcePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func validateCatalogSourcePath(pkg domain.Package) error {
	if pkg.SourcePath == "" {
		return fmt.Errorf("missing source path: kind=%s name=%s", pkg.Kind, pkg.Name)
	}
	repo := RepoCore
	if pkg.Kind != domain.KindFormula {
		repo = RepoCask
	}
	identifier, ok := IdentifierFromPath(repo, pkg.SourcePath)
	if !ok || identifier != pkg.Name {
		return fmt.Errorf("unsupported source path: kind=%s name=%s path=%q maps=%q ok=%v", pkg.Kind, pkg.Name, pkg.SourcePath, identifier, ok)
	}
	if strings.HasPrefix(pkg.SourcePath, "Casks/font/font-") && pkg.Kind != domain.KindFont {
		return fmt.Errorf("nested font path is not classified as font: kind=%s name=%s path=%q", pkg.Kind, pkg.Name, pkg.SourcePath)
	}
	return nil
}

func TestOfficialRemotesExposeDefaultBranch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	g := ExecGit{}
	for _, remote := range []string{CoreRemote, CaskRemote} {
		out, e := g.Run(ctx, "", "ls-remote", "--symref", remote, "HEAD")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = parseDefaultBranch(strings.TrimSpace(string(out)) + "\n"); e != nil {
			t.Fatal(e)
		}
	}
}
