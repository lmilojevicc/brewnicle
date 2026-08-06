package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/milo/brewnicle/internal/domain"
)

func TestGoldenViews(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	added := now.Add(-10 * 24 * time.Hour)
	packages := []domain.Package{
		{Name: "ripgrep", Kind: domain.KindFormula, Description: "Fast line-oriented search tool that recursively searches directories.", Homepage: "https://github.com/BurntSushi/ripgrep", InstallTarget: "ripgrep", AddedAt: &added},
		{Name: "font-maple", Kind: domain.KindFont, Description: "Rounded monospace programming font", InstallTarget: "font-maple", AddedAt: &added},
	}
	available := Dependencies{Install: func(domain.Package) tea.Cmd { return nil }}
	base := func(w, h int) Model {
		m := New(packages, false, false, true, available)
		m.now = func() time.Time { return now }
		return update(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	}
	cases := map[string]Model{
		"wide": base(100, 20), "narrow-list": base(60, 15), "narrow-min-list": base(50, 12), "too-small": base(49, 11),
	}
	narrowDetail := base(60, 15)
	narrowDetail.state = StateNarrowDetail
	cases["narrow-detail"] = narrowDetail
	narrowMinDetail := base(50, 12)
	narrowMinDetail.state = StateNarrowDetail
	cases["narrow-min-detail"] = narrowMinDetail
	search := base(100, 16)
	search = update(t, search, runeKey("/"))
	cases["search"] = search
	narrowSearch := New(packages, false, true, true, available)
	narrowSearch.now = func() time.Time { return now }
	narrowSearch = update(t, narrowSearch, tea.WindowSizeMsg{Width: 50, Height: 12})
	narrowSearch.refreshing = true
	narrowSearch.progress = "fetching a long repository description"
	narrowSearch = update(t, narrowSearch, runeKey("/"))
	cases["narrow-search-stale-refresh"] = narrowSearch
	bootstrap := New(nil, true, false, true, available)
	bootstrap = update(t, bootstrap, tea.WindowSizeMsg{Width: 100, Height: 16})
	cases["bootstrap"] = bootstrap
	confirm := base(100, 16)
	confirm.state = StateConfirm
	cases["confirm"] = confirm
	stale := New(packages, false, true, true, available)
	stale.now = func() time.Time { return now }
	stale = update(t, stale, tea.WindowSizeMsg{Width: 100, Height: 16})
	cases["stale"] = stale
	empty := New([]domain.Package{{Name: "unknown", Kind: domain.KindFormula}}, false, false, true, available)
	empty = update(t, empty, tea.WindowSizeMsg{Width: 100, Height: 16})
	cases["empty"] = empty
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", "golden", name+".golden")
			got := []byte(normalizeGolden(m.View()))
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(got) {
				t.Fatalf("golden mismatch; run UPDATE_GOLDEN=1 go test ./internal/ui -run TestGoldenViews")
			}
		})
	}
}

func normalizeGolden(view string) string {
	lines := strings.Split(view, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestNormalizeGolden(t *testing.T) {
	input := "  leading and  internal  \t\n\nnext\t \n\n"
	want := "  leading and  internal\n\nnext\n"
	if got := normalizeGolden(input); got != want {
		t.Fatalf("normalizeGolden() = %q, want %q", got, want)
	}
}
