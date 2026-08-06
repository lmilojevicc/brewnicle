package history

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/milo/brewnicle/internal/domain"
)

type gitFixture struct {
	t   *testing.T
	bin string
	dir string
}

func newGitFixture(t *testing.T) gitFixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	fixture := gitFixture{t: t, bin: git, dir: t.TempDir()}
	fixture.run(nil, "init", "-q")
	fixture.run(nil, "config", "user.email", "test@example.com")
	fixture.run(nil, "config", "user.name", "Test")
	return fixture
}

func (f gitFixture) run(env []string, args ...string) {
	f.t.Helper()
	cmd := exec.Command(f.bin, args...)
	cmd.Dir = f.dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func (f gitFixture) commit(date, message string) {
	f.t.Helper()
	f.run([]string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}, "commit", "-qam", message)
}

func TestScannerAgainstTemporaryGitRepository(t *testing.T) {
	fixture := newGitFixture(t)
	if err := os.MkdirAll(filepath.Join(fixture.dir, "Formula"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.dir, "Formula", "demo.rb"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	fixture.run(nil, "add", ".")
	fixture.commit("2020-01-01T00:00:00Z", "add")
	if err := os.MkdirAll(filepath.Join(fixture.dir, "Formula", "d"), 0755); err != nil {
		t.Fatal(err)
	}
	fixture.run(nil, "mv", "Formula/demo.rb", "Formula/d/demo.rb")
	fixture.commit("2021-01-01T00:00:00Z", "bucket")
	events, err := (Scanner{Git: fixture.bin}).Scan(context.Background(), filepath.Join(fixture.dir, ".git"), "HEAD", RepoCore)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if !events["demo"].Equal(want) {
		t.Fatalf("got %v want %v", events["demo"], want)
	}
}

func TestScannerCaskFontFormerNameAndDeleteReadd(t *testing.T) {
	fixture := newGitFixture(t)
	casks := filepath.Join(fixture.dir, "Casks")
	if err := os.MkdirAll(casks, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(casks, "old-token.rb"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	fixture.run(nil, "add", ".")
	fixture.commit("2019-01-01T00:00:00Z", "old token")
	fixture.run(nil, "rm", "Casks/old-token.rb")
	fixture.commit("2020-01-01T00:00:00Z", "delete")
	if err := os.MkdirAll(casks, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(casks, "old-token.rb"), []byte("x2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(casks, "font-demo.rb"), []byte("font"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(casks, "new-token.rb"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	fixture.run(nil, "add", ".")
	fixture.commit("2022-01-01T00:00:00Z", "readd and current names")
	events, err := (Scanner{Git: fixture.bin}).Scan(context.Background(), filepath.Join(fixture.dir, ".git"), "HEAD", RepoCask)
	if err != nil {
		t.Fatal(err)
	}
	if !events["old-token"].Equal(time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("delete/re-add lost earliest event", events)
	}
	if events["font-demo"].IsZero() {
		t.Fatal("font cask event missing", events)
	}
	packages := []domain.Package{{Name: "new-token", Kind: domain.KindCask, FormerNames: []string{"old-token"}}}
	resolved := Resolve(packages, nil, events, time.Now())
	if resolved[0].AddedAt == nil || !resolved[0].AddedAt.Equal(events["old-token"]) {
		t.Fatal("former name did not resolve earliest event", resolved[0])
	}
}

func TestScannerCurrentLibAndFontPathsAppearInBoundedFilters(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	coreFixture := newGitFixture(t)
	libDir := filepath.Join(coreFixture.dir, "Formula", "lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libjaylink.rb"), []byte("formula"), 0644); err != nil {
		t.Fatal(err)
	}
	coreFixture.run(nil, "add", ".")
	coreFixture.commit("2026-07-27T12:00:00Z", "add lib formula")
	coreEvents, err := (Scanner{Git: coreFixture.bin}).Scan(context.Background(), filepath.Join(coreFixture.dir, ".git"), "HEAD", RepoCore)
	if err != nil {
		t.Fatal(err)
	}

	caskFixture := newGitFixture(t)
	fontDir := filepath.Join(caskFixture.dir, "Casks", "font", "font-n")
	if err := os.MkdirAll(fontDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fontDir, "font-nexon-lv2-gothic.rb"), []byte("font"), 0644); err != nil {
		t.Fatal(err)
	}
	caskFixture.run(nil, "add", ".")
	caskFixture.commit("2026-08-05T12:00:00Z", "add font")
	caskEvents, err := (Scanner{Git: caskFixture.bin}).Scan(context.Background(), filepath.Join(caskFixture.dir, ".git"), "HEAD", RepoCask)
	if err != nil {
		t.Fatal(err)
	}

	packages := []domain.Package{
		{Name: "libjaylink", Kind: domain.KindFormula},
		{Name: "font-nexon-lv2-gothic", Kind: domain.KindFont},
	}
	resolved := Resolve(packages, coreEvents, caskEvents, now)
	if got := domain.Filter(resolved, domain.Range7D, "", now); len(got) != 1 || got[0].Name != "font-nexon-lv2-gothic" {
		t.Fatalf("7d filter = %+v", got)
	}
	if got := domain.Filter(resolved, domain.Range30D, "", now); len(got) != 2 {
		t.Fatalf("30d filter = %+v", got)
	}
}

func TestScannerFailedGitCommand(t *testing.T) {
	_, err := (Scanner{Git: filepath.Join(t.TempDir(), "missing-git")}).Scan(context.Background(), t.TempDir(), "HEAD", RepoCore)
	if err == nil {
		t.Fatal("missing git command succeeded")
	}
}
