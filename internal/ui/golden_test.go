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
		m.applyFilter("")
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
	narrowEmpty := base(50, 12)
	narrowEmpty.state = StateNarrowDetail
	narrowEmpty = update(t, narrowEmpty, runeKey("f")) // formula
	narrowEmpty = update(t, narrowEmpty, runeKey("f")) // cask: no matches
	if narrowEmpty.State() != StateBrowse {
		t.Fatal("empty narrow detail did not return to list")
	}
	cases["narrow-empty"] = narrowEmpty
	kindFormula := base(100, 16)
	kindFormula = update(t, kindFormula, runeKey("f"))
	cases["kind-formula"] = kindFormula

	edge := edgePackages(now)
	edgeBase := func(w, h int) Model {
		m := New(edge, false, false, true, available)
		m.now = func() time.Time { return now }
		m.rangeValue = domain.RangeAll
		m.applyFilter("")
		return update(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	}
	cases["wide-long-names"] = edgeBase(120, 24)
	cases["narrow-long-names"] = edgeBase(60, 15)
	edgeLongCask := edgeBase(90, 24)
	edgeLongCask.selected = 1
	cases["wide-long-cask-detail"] = edgeLongCask
	confirmLong := New([]domain.Package{{Name: "font-jetbrains-mono-nerd-font", Kind: domain.KindFont, InstallTarget: "font-jetbrains-mono-nerd-font", AddedAt: &added}}, false, false, true, available)
	confirmLong.now = func() time.Time { return now }
	confirmLong.applyFilter("")
	confirmLong = update(t, confirmLong, tea.WindowSizeMsg{Width: 50, Height: 12})
	confirmLong = update(t, confirmLong, runeKey("i"))
	cases["confirm-wide-command"] = confirmLong

	rich := richWidePackages(now)
	richBase := func() Model {
		m := New(rich, false, false, true, available)
		m.now = func() time.Time { return now }
		m.rangeValue = domain.RangeAll
		m.applyFilter("")
		return update(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	}
	cases["wide-rich"] = richBase()
	search := base(100, 16)
	search = update(t, search, runeKey("/"))
	cases["search"] = search
	narrowSearch := New(packages, false, true, true, available)
	narrowSearch.now = func() time.Time { return now }
	narrowSearch.applyFilter("")
	narrowSearch = update(t, narrowSearch, tea.WindowSizeMsg{Width: 50, Height: 12})
	narrowSearch.refreshing = true
	narrowSearch.progress = "fetching a long repository description"
	narrowSearch = update(t, narrowSearch, runeKey("/"))
	cases["narrow-search-stale-refresh"] = narrowSearch
	bootstrap := New(nil, true, false, true, available)
	bootstrap = update(t, bootstrap, tea.WindowSizeMsg{Width: 100, Height: 16})
	cases["bootstrap"] = bootstrap
	confirm := base(100, 16)
	confirm = update(t, confirm, runeKey("i"))
	cases["confirm"] = confirm
	stale := New(packages, false, true, true, available)
	stale.now = func() time.Time { return now }
	stale.applyFilter("")
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

// richWidePackages mirrors the approved wide mockup fixture: enough packages
// to fill an eighteen-row list with rich rows across every package kind.
func richWidePackages(now time.Time) []domain.Package {
	specs := []struct {
		name string
		kind domain.Kind
		days int
		desc string
		home string
	}{
		{"font-maple", domain.KindFont, 10, "Rounded monospace programming font", ""},
		{"ripgrep", domain.KindFormula, 10, "Fast line-oriented search tool that recursively searches directories.", "https://github.com/BurntSushi/ripgrep"},
		{"k9s", domain.KindCask, 13, "Kubernetes CLI to manage your clusters in style", ""},
		{"neovim", domain.KindFormula, 14, "Vim-fork focused on extensibility and usability", ""},
		{"font-hack", domain.KindFont, 16, "Typeface designed for source code", ""},
		{"bat", domain.KindFormula, 17, "Cat clone with syntax highlighting", ""},
		{"lazygit", domain.KindFormula, 19, "Simple terminal UI for git commands", ""},
		{"zed", domain.KindCask, 22, "High-performance multiplayer code editor", ""},
		{"fd", domain.KindFormula, 25, "Simple, fast and user-friendly find alternative", ""},
		{"jq", domain.KindFormula, 32, "Lightweight and flexible command-line JSON processor", ""},
		{"htop", domain.KindFormula, 34, "Interactive process viewer", ""},
		{"tree", domain.KindFormula, 36, "Display directories as trees", ""},
		{"wget", domain.KindFormula, 38, "Get a file from the web", ""},
		{"curl", domain.KindFormula, 41, "Internet file transfer tool", ""},
		{"tmux", domain.KindFormula, 44, "Terminal multiplexer", ""},
		{"fzf", domain.KindFormula, 47, "Command-line fuzzy finder", ""},
		{"zoxide", domain.KindFormula, 51, "Smarter cd command", ""},
		{"delta", domain.KindFormula, 55, "Syntax-highlighting pager for git", ""},
	}
	out := make([]domain.Package, len(specs))
	for i, sp := range specs {
		at := now.Add(-time.Duration(sp.days) * 24 * time.Hour)
		out[i] = domain.Package{Name: sp.name, Kind: sp.kind, Description: sp.desc, Homepage: sp.home, InstallTarget: sp.name, AddedAt: &at}
	}
	return out
}

// edgePackages pins the row grammar's edges: names longer than twelve and
// twenty cells, mo/y/? ages, and a short description that exercises the
// padding inside the reverse-video selection band.
func edgePackages(now time.Time) []domain.Package {
	specs := []struct {
		name  string
		kind  domain.Kind
		days  int
		dated bool
		desc  string
	}{
		{"kubernetes-cli", domain.KindCask, 3, true, "CLI"},
		{"font-jetbrains-mono-nerd-font", domain.KindFont, 120, true, "Nerd Font patched JetBrains Mono"},
		{"mongodb-community", domain.KindFormula, 400, true, "Document-oriented database"},
		{"terraform", domain.KindFormula, 0, false, "Infrastructure as code"},
	}
	out := make([]domain.Package, len(specs))
	for i, sp := range specs {
		p := domain.Package{Name: sp.name, Kind: sp.kind, Description: sp.desc, InstallTarget: sp.name}
		if sp.dated {
			at := now.Add(-time.Duration(sp.days) * 24 * time.Hour)
			p.AddedAt = &at
		}
		out[i] = p
	}
	return out
}

// richWidePackages mirrors the approved wide mockup fixture: enough packages
// to fill an eighteen-row list with rich rows across every package kind.
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
