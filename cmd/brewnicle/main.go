package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/milo/brewnicle/internal/catalog"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/history"
	"github.com/milo/brewnicle/internal/platform"
	"github.com/milo/brewnicle/internal/refresh"
	"github.com/milo/brewnicle/internal/store"
	"github.com/milo/brewnicle/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "brewnicle:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	model, err := newApplication(ctx, "")
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}

type applicationHooks struct {
	now             func() time.Time
	preserveInvalid func(string, time.Time) (string, error)
	lookPath        platform.LookupFunc
	locker          refresh.Locker
}

func defaultApplicationHooks() applicationHooks {
	return applicationHooks{
		now:             time.Now,
		preserveInvalid: store.PreserveInvalid,
		lookPath:        exec.LookPath,
	}
}

func newApplication(ctx context.Context, cacheRoot string) (ui.Model, error) {
	return newApplicationWithHooks(ctx, cacheRoot, defaultApplicationHooks())
}

func newApplicationWithHooks(ctx context.Context, cacheRoot string, hooks applicationHooks) (ui.Model, error) {
	defaults := defaultApplicationHooks()
	if hooks.now == nil {
		hooks.now = defaults.now
	}
	if hooks.preserveInvalid == nil {
		hooks.preserveInvalid = defaults.preserveInvalid
	}
	if hooks.lookPath == nil {
		hooks.lookPath = defaults.lookPath
	}
	paths, err := refresh.ResolvePaths(cacheRoot)
	if err != nil {
		return ui.Model{}, err
	}
	locker := hooks.locker
	if locker == nil {
		locker = refresh.FileLock{Path: paths.Lock}
	}
	snapshot, bootstrap, bootstrapDiagnostic, err := loadStartupIndex(ctx, paths.Index, locker, hooks.now(), hooks.preserveInvalid)
	if err != nil {
		return ui.Model{}, err
	}
	stale := !bootstrap && refresh.NeedsRefresh(snapshot, hooks.now())
	client := catalog.NewClient(&http.Client{Timeout: 2 * time.Minute})
	historyCache := &history.Cache{Boundary: paths.Root, Root: paths.Git, Git: history.ExecGit{}, Scanner: history.Scanner{}}
	publisher := store.Publisher{Path: paths.Index}
	var visibleSnapshot *store.Snapshot
	if !bootstrap {
		visibleSnapshot = &snapshot
	}
	service := refresh.Service{Catalog: client, History: historyCache, Index: publisher, Locker: locker, Now: hooks.now, CatalogFirst: bootstrap, VisibleSnapshot: visibleSnapshot}
	starter := func() <-chan ui.RefreshEvent {
		ch := make(chan ui.RefreshEvent, 16)
		go func() {
			defer close(ch)
			emit := func(p refresh.Progress) {
				event := ui.RefreshEvent{Progress: &p}
				switch p.Phase {
				case refresh.PhaseCatalogReady:
					event.CatalogReady = true
					event.Packages = p.Packages
				case refresh.PhaseSnapshotReady:
					event.SnapshotReady = true
					event.Packages = p.Packages
				}
				select {
				case ch <- event:
				case <-ctx.Done():
				}
			}
			result, summary, runErr := service.Run(ctx, emit)
			event := ui.RefreshEvent{Summary: summary, Err: runErr, Done: true}
			if runErr == nil {
				event.Packages = result.Snapshot.Packages
			}
			select {
			case ch <- event:
			case <-ctx.Done():
			}
		}()
		return ch
	}
	deps := ui.Dependencies{
		Refresh:             starter,
		BootstrapDiagnostic: bootstrapDiagnostic,
		Open: func(p domain.Package) tea.Cmd {
			return func() tea.Msg {
				err := platform.Open(ctx, p, "", nil, nil)
				return ui.ActionResultMsg{Action: "Open homepage", Err: err}
			}
		},
	}
	brewPath, brewErr := hooks.lookPath("brew")
	if brewErr == nil && brewPath != "" {
		deps.Install = func(p domain.Package) tea.Cmd {
			finder := func(string) (string, error) { return brewPath, nil }
			cmd, buildErr := platform.BuildInstallCommand(ctx, p, finder)
			if buildErr != nil {
				return func() tea.Msg { return platform.ExecResult{Err: buildErr} }
			}
			return platform.TeaExec(cmd)
		}
	} else {
		deps.InstallUnavailable = "Homebrew is unavailable"
	}
	return ui.New(snapshot.Packages, bootstrap, stale, os.Getenv("NO_COLOR") != "", deps), nil
}

func loadStartupIndex(ctx context.Context, path string, locker refresh.Locker, now time.Time, preserve func(string, time.Time) (string, error)) (snapshot store.Snapshot, bootstrap bool, diagnostic string, err error) {
	snapshot, loadErr := store.Load(path)
	if loadErr == nil {
		return snapshot, false, "", nil
	}
	initialInfo, statErr := os.Stat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return store.Snapshot{}, true, "", nil
	}
	if statErr != nil {
		return store.Snapshot{}, false, "", statErr
	}
	release, err := locker.Acquire(ctx, nil)
	if err != nil {
		return store.Snapshot{}, false, "", err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()

	// Another process may have atomically published while this process waited.
	// Reload under the publication lock before touching the active path.
	lockedSnapshot, lockedErr := store.Load(path)
	if lockedErr == nil {
		return lockedSnapshot, false, "", nil
	}
	if errors.Is(lockedErr, store.ErrNotFound) {
		return store.Snapshot{}, true, "", nil
	}
	lockedInfo, statErr := os.Stat(path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return store.Snapshot{}, true, "", nil
		}
		return store.Snapshot{}, false, "", statErr
	}
	if !os.SameFile(initialInfo, lockedInfo) {
		return store.Snapshot{}, false, "", fmt.Errorf("existing index changed while its invalid state was being verified")
	}
	preserved, preserveErr := preserve(path, now)
	if preserveErr != nil {
		return store.Snapshot{}, false, "", fmt.Errorf("existing index is invalid (%v); preserving it failed: %w", lockedErr, preserveErr)
	}
	return store.Snapshot{}, true, fmt.Sprintf("Existing index was invalid (%v) and preserved at %s", lockedErr, preserved), nil
}
