package platform

import (
	"context"
	"errors"
	"github.com/milo/brewnicle/internal/domain"
	"reflect"
	"testing"
)

func finder(name string) (string, error) { return "/bin/" + name, nil }
func TestInstallCommands(t *testing.T) {
	for _, tc := range []struct {
		p    domain.Package
		args []string
	}{
		{domain.Package{Name: "ripgrep", InstallTarget: "ripgrep", Kind: domain.KindFormula}, []string{"/bin/brew", "install", "ripgrep"}},
		{domain.Package{Name: "font-x", InstallTarget: "font-x", Kind: domain.KindFont}, []string{"/bin/brew", "install", "--cask", "font-x"}},
		{domain.Package{Name: "fallback", Kind: domain.KindFormula}, []string{"/bin/brew", "install", "fallback"}},
	} {
		cmd, e := BuildInstallCommand(context.Background(), tc.p, finder)
		if e != nil || !reflect.DeepEqual(cmd.Args, tc.args) || cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
			t.Fatal(cmd, e)
		}
	}
}
func TestOpenCommands(t *testing.T) {
	p := domain.Package{Homepage: "https://example.test/x"}
	for _, tc := range []struct {
		os   string
		want string
	}{{"darwin", "/bin/open"}, {"linux", "/bin/xdg-open"}} {
		cmd, e := BuildOpenCommand(context.Background(), p, tc.os, finder)
		if e != nil || cmd.Path != tc.want || len(cmd.Args) != 2 {
			t.Fatal(cmd, e)
		}
	}
	for _, u := range []string{"", "file:///x", "javascript:x"} {
		p.Homepage = u
		if _, e := BuildOpenCommand(context.Background(), p, "darwin", finder); e == nil {
			t.Errorf("accepted %q", u)
		}
	}
}
func TestMissingBrew(t *testing.T) {
	p := domain.Package{Name: "x", InstallTarget: "x", Kind: domain.KindFormula}
	if _, e := BuildInstallCommand(context.Background(), p, func(string) (string, error) { return "", errors.New("missing") }); e == nil {
		t.Fatal()
	}
}
