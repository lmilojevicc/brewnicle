package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/milo/brewnicle/internal/domain"
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
		{"fresh", now.Add(-23 * time.Hour), false},
		{"stale", now.Add(-24 * time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "cache")
			added := now.Add(-time.Hour)
			packages := []domain.Package{{Name: "x", Kind: domain.KindFormula, InstallTarget: "x", AddedAt: &added, UpdatedAt: now}}
			if _, err := (store.Publisher{Path: filepath.Join(root, "index.db")}).Publish(context.Background(), packages, tc.refreshed); err != nil {
				t.Fatal(err)
			}
			m, err := newApplicationWithHooks(context.Background(), root, testHooks(now))
			if err != nil || m.State() != ui.StateBrowse || m.Stale() != tc.stale || m.PendingRefresh() != tc.stale {
				t.Fatal(m.State(), m.Stale(), m.PendingRefresh(), err)
			}
		})
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
	if _, err := (store.Publisher{Path: filepath.Join(root, "index.db")}).Publish(context.Background(), packages, now); err != nil {
		t.Fatal(err)
	}
}
