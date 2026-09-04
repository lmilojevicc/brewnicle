package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/history"
	"github.com/milo/brewnicle/internal/refresh"
	"github.com/milo/brewnicle/internal/store"
	"github.com/milo/brewnicle/internal/ui"
)

func testHooks(now time.Time) applicationHooks {
	return applicationHooks{
		now:             func() time.Time { return now },
		preserveInvalid: store.PreserveInvalid,
		lookPath:        func(string) (string, error) { return "", errors.New("missing") },
	}
}

type applicationLocker func(context.Context, func()) (refresh.ReleaseFunc, error)

func (f applicationLocker) Acquire(ctx context.Context, waiting func()) (refresh.ReleaseFunc, error) {
	return f(ctx, waiting)
}

func TestMissingIndexStartsBootstrapAfterVisibleFrame(t *testing.T) {
	m, err := newApplicationWithHooks(context.Background(), filepath.Join(t.TempDir(), "cache"), testHooks(time.Now()))
	if err != nil || m.State() != ui.StateBootstrap || !m.PendingRefresh() || m.Init() != nil {
		t.Fatal(m.State(), m.PendingRefresh(), err)
	}
	model, frameCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = model.(ui.Model)
	if frameCmd == nil || !m.PendingRefresh() || !strings.Contains(m.View(), "building first index") {
		t.Fatal("first visible frame did not schedule the post-render boundary")
	}
	model, startCmd := m.Update(frameCmd())
	m = model.(ui.Model)
	if startCmd == nil || m.PendingRefresh() {
		t.Fatal("post-render boundary did not queue bootstrap")
	}
}

func TestFreshAndStaleIndexStartupIntent(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		refreshed time.Time
		stale     bool
	}{
		{"fresh current", now.Add(-23 * time.Hour), false},
		{"stale current", now.Add(-24 * time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "cache")
			added := now.Add(-time.Hour)
			packages := []domain.Package{{Name: "x", Kind: domain.KindFormula, InstallTarget: "x", AddedAt: &added, UpdatedAt: now}}
			indexPath := filepath.Join(root, "index.db")
			if err := publishTestIndex(indexPath, packages, tc.refreshed); err != nil {
				t.Fatal(err)
			}
			m, err := newApplicationWithHooks(context.Background(), root, testHooks(now))
			if err != nil || m.State() != ui.StateBrowse || m.Stale() != tc.stale || m.PendingRefresh() != tc.stale {
				t.Fatal(m.State(), m.Stale(), m.PendingRefresh(), err)
			}
		})
	}
}

func publishTestIndex(path string, packages []domain.Package, at time.Time) error {
	coreEvents := map[string]time.Time{"core-placeholder": time.Unix(1, 0)}
	caskEvents := map[string]time.Time{"cask-placeholder": time.Unix(1, 0)}
	for _, pkg := range packages {
		if pkg.Kind == domain.KindFormula {
			coreEvents[pkg.Name] = at
		} else {
			caskEvents[pkg.Name] = at
		}
	}
	state := history.State{
		Core: history.RepoState{Repo: history.RepoCore, RemoteURL: history.CoreRemote, BranchRef: "refs/heads/main", TipOID: strings.Repeat("a", 40), AlgorithmVersion: history.AlgorithmVersion, Complete: true, Events: coreEvents},
		Cask: history.RepoState{Repo: history.RepoCask, RemoteURL: history.CaskRemote, BranchRef: "refs/heads/main", TipOID: strings.Repeat("b", 40), AlgorithmVersion: history.AlgorithmVersion, Complete: true, Events: caskEvents},
	}
	_, err := (store.Publisher{Path: path}).Publish(context.Background(), store.PublishInput{Packages: packages, History: state, RefreshedAt: at})
	return err
}

func TestLegacyV1IndexRemainsVisibleDuringBackgroundMigration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE packages(name TEXT NOT NULL,kind TEXT NOT NULL,description TEXT NOT NULL,homepage TEXT NOT NULL,added_at INTEGER,install_target TEXT NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(kind,name)); CREATE TABLE metadata(key TEXT PRIMARY KEY NOT NULL,value TEXT NOT NULL); INSERT INTO packages VALUES('legacy','formula','visible','','1','legacy','2'); INSERT INTO metadata VALUES('schema_version','1'),('last_successful_refresh','3'),('history_layout_version','2');`)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	m, err := newApplicationWithHooks(context.Background(), root, testHooks(time.Unix(100, 0)))
	if err != nil || m.State() != ui.StateBrowse || !m.PendingRefresh() || !m.Stale() {
		t.Fatal(m.State(), m.PendingRefresh(), m.Stale(), err)
	}
	snapshot, err := store.Load(path)
	if err != nil || snapshot.SchemaVersion != 1 || len(snapshot.Packages) != 1 {
		t.Fatal(snapshot, err)
	}
}

func TestCorruptIndexIsPreservedWithVisibleDiagnostic(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.db"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := newApplicationWithHooks(context.Background(), root, testHooks(time.Unix(0, 0)))
	if err != nil || m.State() != ui.StateBootstrap {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "index.db.corrupt-*"))
	if len(matches) != 1 {
		t.Fatal(matches)
	}
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if view := model.(ui.Model).View(); !strings.Contains(view, "invalid") || !strings.Contains(view, "preserved at") {
		t.Fatal(view)
	}
}

func TestCorruptIndexPreservationFailureIsReturned(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.db"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	hooks := testHooks(time.Now())
	hooks.preserveInvalid = func(string, time.Time) (string, error) { return "", errors.New("rename denied") }
	_, err := newApplicationWithHooks(context.Background(), root, hooks)
	if err == nil || !strings.Contains(err.Error(), "invalid") || !strings.Contains(err.Error(), "rename denied") {
		t.Fatal(err)
	}
}

func TestWinningIndexPublishedBeforeLockedPreservationRemainsActive(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "index.db")
	if err := os.WriteFile(indexPath, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	added := now.Add(-time.Hour)
	winner := []domain.Package{{Name: "winner", Kind: domain.KindFormula, InstallTarget: "winner", AddedAt: &added, UpdatedAt: now}}
	preserved := false
	hooks := testHooks(now)
	hooks.preserveInvalid = func(string, time.Time) (string, error) {
		preserved = true
		return "", errors.New("must not preserve winner")
	}
	hooks.locker = applicationLocker(func(context.Context, func()) (refresh.ReleaseFunc, error) {
		if err := publishTestIndex(indexPath, winner, now); err != nil {
			t.Fatal(err)
		}
		return func() error { return nil }, nil
	})
	m, err := newApplicationWithHooks(context.Background(), root, hooks)
	if err != nil || preserved || m.State() != ui.StateBrowse || len(m.Visible()) != 1 || m.Visible()[0].Name != "winner" {
		t.Fatal(err, preserved, m.State(), m.Visible())
	}
	loaded, err := store.Load(indexPath)
	if err != nil || len(loaded.Packages) != 1 || loaded.Packages[0].Name != "winner" {
		t.Fatal(loaded, err)
	}
	matches, _ := filepath.Glob(indexPath + ".corrupt-*")
	if len(matches) != 0 {
		t.Fatal(matches)
	}
}

// TestWriteSmokeFixture is an opt-in developer helper, not a product cache flag.
func TestWriteSmokeFixture(t *testing.T) {
	root := os.Getenv("BREWNICLE_SMOKE_CACHE")
	if root == "" {
		t.Skip("set BREWNICLE_SMOKE_CACHE to write an isolated fixture")
	}
	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)
	packages := []domain.Package{
		{Name: "ripgrep", Kind: domain.KindFormula, Description: "Fast line-oriented search tool", Homepage: "https://github.com/BurntSushi/ripgrep", InstallTarget: "ripgrep", AddedAt: &old, UpdatedAt: now},
		{Name: "font-maple", Kind: domain.KindFont, Description: "Monospace programming font", InstallTarget: "font-maple", AddedAt: &old, UpdatedAt: now},
	}
	if err := publishTestIndex(filepath.Join(root, "index.db"), packages, now); err != nil {
		t.Fatal(err)
	}
}
