package refresh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/milo/brewnicle/internal/catalog"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/history"
	"github.com/milo/brewnicle/internal/store"
)

type fakeCat struct{ err error }

func (f fakeCat) Fetch(context.Context) (catalog.Result, error) {
	return catalog.Result{Packages: []domain.Package{{Name: "new", Kind: domain.KindFormula, FormerNames: []string{"old"}}}, SkippedFormulae: 2}, f.err
}

type fakeHist struct {
	prepared       history.Prepared
	err, reconcile error
	called         *bool
	onPrepare      func(history.State)
}

func (f fakeHist) PrepareAll(_ context.Context, previous history.State, _ history.ProgressFunc) (history.Prepared, error) {
	if f.called != nil {
		*f.called = true
	}
	if f.onPrepare != nil {
		f.onPrepare(previous)
	}
	return f.prepared, f.err
}
func (f fakeHist) ReconcilePublished(context.Context, history.Prepared) error { return f.reconcile }

type captureHist struct {
	prepared history.Prepared
	seen     *history.State
}

func (f captureHist) PrepareAll(_ context.Context, previous history.State, _ history.ProgressFunc) (history.Prepared, error) {
	if f.seen != nil {
		*f.seen = previous
	}
	return f.prepared, nil
}
func (captureHist) ReconcilePublished(context.Context, history.Prepared) error { return nil }

type fakeLocker struct {
	releaseErr error
	onAcquire  func()
}

func (f fakeLocker) Acquire(context.Context, func()) (ReleaseFunc, error) {
	if f.onAcquire != nil {
		f.onAcquire()
	}
	return func() error { return f.releaseErr }, nil
}

type fakeIndex struct {
	snapshot            store.Snapshot
	loadErr, publishErr error
	published           *store.PublishInput
	onLoad              func()
}

func (f *fakeIndex) Load() (store.Snapshot, error) {
	if f.onLoad != nil {
		f.onLoad()
	}
	return f.snapshot, f.loadErr
}
func (f *fakeIndex) Publish(_ context.Context, in store.PublishInput) (store.PublishResult, error) {
	if f.published != nil {
		*f.published = in
	}
	if f.publishErr != nil {
		return store.PublishResult{}, f.publishErr
	}
	return store.PublishResult{Snapshot: store.Snapshot{SchemaVersion: 2, Packages: in.Packages, RefreshedAt: in.RefreshedAt, HistoryLayoutVersion: store.HistoryLayoutVersion, History: in.History}}, nil
}
func completePrepared() history.Prepared {
	state := history.State{Core: history.RepoState{Repo: history.RepoCore, RemoteURL: history.CoreRemote, BranchRef: "refs/heads/main", TipOID: strings.Repeat("a", 40), AlgorithmVersion: history.AlgorithmVersion, Complete: true, Events: map[string]time.Time{"old": time.Unix(1, 0)}}, Cask: history.RepoState{Repo: history.RepoCask, RemoteURL: history.CaskRemote, BranchRef: "refs/heads/main", TipOID: strings.Repeat("b", 40), AlgorithmVersion: history.AlgorithmVersion, Complete: true, Events: map[string]time.Time{"c": time.Unix(1, 0)}}}
	return history.Prepared{State: state}
}

func writeLegacyIndex(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE packages(name TEXT NOT NULL,kind TEXT NOT NULL,description TEXT NOT NULL,homepage TEXT NOT NULL,added_at INTEGER,install_target TEXT NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(kind,name))`,
		`CREATE TABLE metadata(key TEXT PRIMARY KEY NOT NULL,value TEXT NOT NULL)`,
		`INSERT INTO packages(name,kind,description,homepage,added_at,install_target,updated_at) VALUES('legacy','formula','old','','1','legacy','1')`,
		`INSERT INTO metadata(key,value) VALUES('schema_version','1'),('last_successful_refresh','1'),('history_layout_version','2')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

type blockingHist struct {
	started    chan struct{}
	release    chan struct{}
	prepared   history.Prepared
	err        error
	reconciled *bool
}

func (h *blockingHist) PrepareAll(context.Context, history.State, history.ProgressFunc) (history.Prepared, error) {
	close(h.started)
	<-h.release
	return h.prepared, h.err
}
func (h *blockingHist) ReconcilePublished(context.Context, history.Prepared) error {
	if h.reconciled != nil {
		*h.reconciled = true
	}
	return nil
}

type migrationHist struct {
	state     history.State
	fullScans int
}

func TestBootstrapEmitsCatalogBeforeBlockedHistoryWithoutPublishingIt(t *testing.T) {
	reconciled := false
	hist := &blockingHist{started: make(chan struct{}), release: make(chan struct{}), prepared: completePrepared(), reconciled: &reconciled}
	var published store.PublishInput
	idx := &fakeIndex{loadErr: store.ErrNotFound, published: &published}
	s := Service{Catalog: fakeCat{}, History: hist, Index: idx, Now: func() time.Time { return time.Unix(2, 0) }, CatalogFirst: true}
	events := make(chan Progress, 16)
	finished := make(chan error, 1)
	go func() {
		_, _, err := s.Run(context.Background(), func(event Progress) { events <- event })
		finished <- err
	}()
	select {
	case <-hist.started:
	case <-time.After(time.Second):
		t.Fatal("history did not start")
	}
	var phases []Phase
	var catalog []domain.Package
	drain := true
	for drain {
		select {
		case event := <-events:
			phases = append(phases, event.Phase)
			if event.Phase == PhaseCatalogReady {
				catalog = event.Packages
			}
		default:
			drain = false
		}
	}
	readyAt, historyAt := -1, -1
	for i, phase := range phases {
		if phase == PhaseCatalogReady {
			readyAt = i
		}
		if phase == PhaseHistory && historyAt == -1 {
			historyAt = i
		}
	}
	if readyAt == -1 || historyAt == -1 || readyAt >= historyAt || len(catalog) != 1 || catalog[0].Name != "new" {
		t.Fatalf("events=%v catalog=%v", phases, catalog)
	}
	if published.Packages != nil || published.History.CompleteCurrent() || reconciled {
		t.Fatal("provisional catalog was published or reconciled")
	}
	close(hist.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestValidCachedGenerationDoesNotEmitProvisionalCatalog(t *testing.T) {
	prepared := completePrepared()
	idx := &fakeIndex{snapshot: store.Snapshot{
		SchemaVersion:        store.CurrentSchemaVersion,
		HistoryLayoutVersion: store.HistoryLayoutVersion,
		History:              prepared.State,
		Packages:             []domain.Package{{Name: "cached", Kind: domain.KindFormula}},
	}}
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: prepared}, Index: idx, CatalogFirst: true}
	var phases []Phase
	if _, _, err := s.Run(context.Background(), func(event Progress) { phases = append(phases, event.Phase) }); err != nil {
		t.Fatal(err)
	}
	for _, phase := range phases {
		if phase == PhaseCatalogReady {
			t.Fatalf("valid cache was replaced provisionally: %v", phases)
		}
	}
}

func TestBootstrapExposesWinningSnapshotBeforeCatalogFailureOnlyOnce(t *testing.T) {
	prepared := completePrepared()
	called := false
	idx := &fakeIndex{snapshot: store.Snapshot{
		SchemaVersion:        store.CurrentSchemaVersion,
		HistoryLayoutVersion: store.HistoryLayoutVersion,
		History:              prepared.State,
		Packages:             []domain.Package{{Name: "winner", Kind: domain.KindFormula}},
	}}
	s := Service{Catalog: fakeCat{err: errors.New("catalog down")}, History: fakeHist{called: &called}, Index: idx, CatalogFirst: true}
	var events []Progress
	if _, _, err := s.Run(context.Background(), func(event Progress) { events = append(events, event) }); err == nil || !strings.Contains(err.Error(), "catalog down") {
		t.Fatal(err)
	}
	if called || len(events) < 2 || events[0].Phase != PhaseSnapshotReady || events[1].Phase != PhaseCatalog || len(events[0].Packages) != 1 || events[0].Packages[0].Name != "winner" {
		t.Fatalf("called=%v events=%v", called, events)
	}
	events = nil
	if _, _, err := s.Run(context.Background(), func(event Progress) { events = append(events, event) }); err == nil || !strings.Contains(err.Error(), "catalog down") {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Phase == PhaseSnapshotReady || event.Phase == PhaseCatalogReady {
			t.Fatalf("bootstrap event repeated after exact snapshot adoption: %v", events)
		}
	}
}

func TestCatalogFirstIsOneShotAfterSuccessfulPublish(t *testing.T) {
	idx := &fakeIndex{loadErr: store.ErrNotFound}
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared()}, Index: idx, CatalogFirst: true}
	for run := 0; run < 2; run++ {
		var phases []Phase
		if _, _, err := s.Run(context.Background(), func(event Progress) { phases = append(phases, event.Phase) }); err != nil {
			t.Fatal(err)
		}
		ready := false
		for _, phase := range phases {
			ready = ready || phase == PhaseCatalogReady
		}
		if ready != (run == 0) {
			t.Fatalf("run %d phases=%v", run+1, phases)
		}
	}
}

func TestCatalogFirstRetryRetainsBootstrapUntilExactPublish(t *testing.T) {
	hist := &fakeHist{err: errors.New("history interrupted")}
	idx := &fakeIndex{loadErr: store.ErrNotFound}
	s := Service{Catalog: fakeCat{}, History: hist, Index: idx, CatalogFirst: true}
	for run := 0; run < 3; run++ {
		var phases []Phase
		_, _, err := s.Run(context.Background(), func(event Progress) { phases = append(phases, event.Phase) })
		if run == 0 {
			if err == nil || !strings.Contains(err.Error(), "history interrupted") {
				t.Fatal(err)
			}
			hist.err = nil
			hist.prepared = completePrepared()
		} else if err != nil {
			t.Fatal(err)
		}
		ready := false
		for _, phase := range phases {
			ready = ready || phase == PhaseCatalogReady
		}
		wantReady := run < 2
		if ready != wantReady {
			t.Fatalf("run %d phases=%v", run+1, phases)
		}
	}
}

func (h *migrationHist) PrepareAll(_ context.Context, previous history.State, _ history.ProgressFunc) (history.Prepared, error) {
	if !previous.CompleteCurrent() {
		h.fullScans += 2
		return history.Prepared{State: history.CloneState(h.state)}, nil
	}
	return history.Prepared{State: history.CloneState(previous)}, nil
}
func (*migrationHist) ReconcilePublished(context.Context, history.Prepared) error { return nil }
func TestSecondServiceReloadsFirstPublishedGenerationUnderSharedLock(t *testing.T) {
	root := t.TempDir()
	idx := store.Publisher{Path: filepath.Join(root, "index.db")}
	lock := FileLock{Path: filepath.Join(root, "refresh.lock")}
	prepared := completePrepared()
	first := Service{Catalog: fakeCat{}, History: fakeHist{prepared: prepared}, Index: idx, Locker: lock, Now: func() time.Time { return time.Unix(2, 0) }}
	if _, _, err := first.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var seen history.State
	second := Service{Catalog: fakeCat{}, History: captureHist{prepared: prepared, seen: &seen}, Index: idx, Locker: lock, Now: func() time.Time { return time.Unix(3, 0) }}
	if _, _, err := second.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if seen.Core.TipOID != prepared.State.Core.TipOID || seen.Cask.TipOID != prepared.State.Cask.TipOID {
		t.Fatal("second service did not observe first publication")
	}
}

func TestNonBootstrapWaiterAdoptsWinningGenerationBeforeCatalogFailureOnlyOnce(t *testing.T) {
	prepared := completePrepared()
	old := store.Snapshot{
		SchemaVersion:        store.CurrentSchemaVersion,
		HistoryLayoutVersion: store.HistoryLayoutVersion,
		History:              prepared.State,
		RefreshedAt:          time.Unix(1, 0),
		Packages:             []domain.Package{{Name: "old", Kind: domain.KindFormula}},
	}
	winner := old
	winner.RefreshedAt = time.Unix(2, 0)
	winner.Packages = []domain.Package{{Name: "winner", Kind: domain.KindFormula}}
	idx := &fakeIndex{snapshot: old}
	locker := fakeLocker{onAcquire: func() { idx.snapshot = winner }}
	s := Service{
		Catalog:         fakeCat{err: errors.New("catalog down")},
		History:         fakeHist{prepared: prepared},
		Index:           idx,
		Locker:          locker,
		VisibleSnapshot: &old,
	}

	var events []Progress
	if _, _, err := s.Run(context.Background(), func(event Progress) { events = append(events, event) }); err == nil || !strings.Contains(err.Error(), "catalog down") {
		t.Fatal(err)
	}
	if len(events) < 2 || events[0].Phase != PhaseSnapshotReady || events[1].Phase != PhaseCatalog || len(events[0].Packages) != 1 || events[0].Packages[0].Name != "winner" {
		t.Fatalf("events=%v", events)
	}

	events = nil
	if _, _, err := s.Run(context.Background(), func(event Progress) { events = append(events, event) }); err == nil || !strings.Contains(err.Error(), "catalog down") {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Phase == PhaseSnapshotReady || event.Phase == PhaseCatalogReady {
			t.Fatalf("winning generation was emitted twice: %v", events)
		}
	}
}

func TestSnapshotGenerationDistinguishesSameSecondPackageChanges(t *testing.T) {
	prepared := completePrepared()
	first := store.Snapshot{SchemaVersion: store.CurrentSchemaVersion, HistoryLayoutVersion: store.HistoryLayoutVersion, History: prepared.State, RefreshedAt: time.Unix(1, 0), Packages: []domain.Package{{Name: "first", Kind: domain.KindFormula}}}
	second := first
	second.Packages = []domain.Package{{Name: "second", Kind: domain.KindFormula}}
	if snapshotGeneration(first) == snapshotGeneration(second) {
		t.Fatal("distinct same-second publications shared an identity")
	}
}

func TestServiceAcquiresLockBeforeReload(t *testing.T) {
	locked := false
	idx := &fakeIndex{loadErr: store.ErrNotFound, onLoad: func() {
		if !locked {
			t.Fatal("index loaded before lock acquisition")
		}
	}}
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared()}, Index: idx, Locker: fakeLocker{onAcquire: func() { locked = true }}, Now: func() time.Time { return time.Unix(2, 0) }}
	if _, _, err := s.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRealPublisherNormalizesClockPrecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	idx := store.Publisher{Path: path}
	clock := time.Unix(20, 123456789)
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared()}, Index: idx, Now: func() time.Time { return clock }}
	r, _, err := s.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := idx.Load()
	if err != nil || got.RefreshedAt.Nanosecond() != 0 || got.RefreshedAt.Unix() != 20 || r.Snapshot.Packages[0].UpdatedAt.Nanosecond() != 0 || r.Snapshot.Packages[0].UpdatedAt.Unix() != 20 {
		t.Fatal(got.RefreshedAt, r.Snapshot.Packages[0].UpdatedAt, err)
	}
}

func TestLegacyMigrationProgressIsExplicit(t *testing.T) {
	idx := &fakeIndex{snapshot: store.Snapshot{SchemaVersion: store.LegacySchemaVersion, Packages: []domain.Package{{Name: "old", Kind: domain.KindFormula}}}}
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared()}, Index: idx, Now: func() time.Time { return time.Unix(2, 0) }}
	var details []string
	if _, _, err := s.Run(context.Background(), func(p Progress) { details = append(details, p.Detail) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(details, "\n"), "one-time full history migration") {
		t.Fatal(details)
	}
}

func TestServicePublishesCompleteGeneration(t *testing.T) {
	called := false
	var published store.PublishInput
	idx := &fakeIndex{loadErr: store.ErrNotFound, published: &published}
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared(), called: &called}, Index: idx, Now: func() time.Time { return time.Unix(2, 0) }}
	r, sum, e := s.Run(context.Background(), nil)
	if e != nil || !called || sum.SkippedFormulae != 2 || r.Snapshot.Packages[0].AddedAt == nil || !published.History.CompleteCurrent() || published.RefreshedAt.Unix() != 2 {
		t.Fatal(r, sum, e)
	}
}
func TestFailureStopsBeforeHistory(t *testing.T) {
	called := false
	s := Service{Catalog: fakeCat{err: errors.New("no")}, History: fakeHist{called: &called}, Index: &fakeIndex{loadErr: store.ErrNotFound}}
	if _, _, e := s.Run(context.Background(), nil); e == nil || called {
		t.Fatal(e, called)
	}
}
func TestPostPublishReleaseWarningKeepsPublishedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	idx := store.Publisher{Path: path}
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared()}, Index: idx, Locker: fakeLocker{releaseErr: errors.New("unlock")}, Now: func() time.Time { return time.Unix(20, 5) }}
	r, sum, e := s.Run(context.Background(), nil)
	loaded, loadErr := idx.Load()
	if e != nil || loadErr != nil || len(r.Snapshot.Packages) == 0 || len(loaded.Packages) == 0 || !strings.Contains(sum.Warning, "unlock") {
		t.Fatal(r, sum, e, loadErr)
	}
}

func TestPrePublishReleaseFailureJoinsRefreshError(t *testing.T) {
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared()}, Index: &fakeIndex{loadErr: store.ErrNotFound, publishErr: errors.New("publish")}, Locker: fakeLocker{releaseErr: errors.New("unlock")}}
	_, _, err := s.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "publish") || !strings.Contains(err.Error(), "unlock") {
		t.Fatal(err)
	}
}

func TestPostPublishReconcileWarning(t *testing.T) {
	s := Service{Catalog: fakeCat{}, History: fakeHist{prepared: completePrepared(), reconcile: errors.New("ref")}, Index: &fakeIndex{loadErr: store.ErrNotFound}}
	r, sum, e := s.Run(context.Background(), nil)
	if e != nil || len(r.Snapshot.Packages) == 0 || !strings.Contains(sum.Warning, "ref") {
		t.Fatal(r, sum, e)
	}
}
func TestLegacyIndexRemainsVisibleWhileMigrationBlocksAndFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	writeLegacyIndex(t, path)
	idx := store.Publisher{Path: path}
	hist := &blockingHist{started: make(chan struct{}), release: make(chan struct{}), err: errors.New("migration failed")}
	s := Service{Catalog: fakeCat{}, History: hist, Index: idx, Now: func() time.Time { return time.Unix(2, 0) }}
	finished := make(chan error, 1)
	go func() {
		_, _, err := s.Run(context.Background(), nil)
		finished <- err
	}()
	select {
	case <-hist.started:
	case <-time.After(time.Second):
		t.Fatal("migration did not start")
	}
	loaded, err := idx.Load()
	if err != nil || loaded.SchemaVersion != store.LegacySchemaVersion || len(loaded.Packages) != 1 || loaded.Packages[0].Name != "legacy" {
		t.Fatal(loaded, err)
	}
	close(hist.release)
	if err = <-finished; err == nil || !strings.Contains(err.Error(), "migration failed") {
		t.Fatal(err)
	}
	loaded, err = idx.Load()
	if err != nil || loaded.SchemaVersion != store.LegacySchemaVersion || loaded.Packages[0].Name != "legacy" {
		t.Fatal(loaded, err)
	}
}

func TestSuccessfulLegacyMigrationThenSameTipAvoidsAnotherFullScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	writeLegacyIndex(t, path)
	idx := store.Publisher{Path: path}
	hist := &migrationHist{state: completePrepared().State}
	s := Service{Catalog: fakeCat{}, History: hist, Index: idx, Now: func() time.Time { return time.Unix(2, 0) }}
	if _, _, err := s.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Unix(3, 0) }
	if _, _, err := s.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := idx.Load()
	if err != nil || loaded.SchemaVersion != store.CurrentSchemaVersion || hist.fullScans != 2 {
		t.Fatal(loaded.SchemaVersion, hist.fullScans, err)
	}
}

func TestPublicationClockIsCapturedAfterHistoryPreparation(t *testing.T) {
	prepared := false
	clockAfterPrepare := false
	hist := fakeHist{prepared: completePrepared(), onPrepare: func(history.State) { prepared = true }}
	s := Service{
		Catalog: fakeCat{}, History: hist, Index: &fakeIndex{loadErr: store.ErrNotFound},
		Now: func() time.Time {
			clockAfterPrepare = prepared
			return time.Unix(20, 987654321)
		},
	}
	r, _, err := s.Run(context.Background(), nil)
	if err != nil || !clockAfterPrepare || r.Snapshot.RefreshedAt.Unix() != 20 || r.Snapshot.RefreshedAt.Nanosecond() != 0 {
		t.Fatal(clockAfterPrepare, r.Snapshot.RefreshedAt, err)
	}
}

func TestRestartLoadsPublishedSnapshotAndRepairsMissingRefs(t *testing.T) {
	root := t.TempDir()
	idx := store.Publisher{Path: filepath.Join(root, "index.db")}
	state := completePrepared().State
	publishedAt := time.Unix(20, 0)
	packages := history.Resolve(fakeCatPackages(), state.Core.Events, state.Cask.Events, publishedAt)
	if _, err := idx.Publish(context.Background(), store.PublishInput{Packages: packages, History: state, RefreshedAt: publishedAt}); err != nil {
		t.Fatal(err)
	}
	loaded, err := idx.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"core.git", "cask.git"} {
		if err = os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[history.RepoKind][]string{}
	cache := history.Cache{Root: root, Git: refreshGitRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 3 && args[2] == "update-ref" {
			repo := history.RepoCore
			if strings.Contains(args[1], "cask.git") {
				repo = history.RepoCask
			}
			seen[repo] = append([]string(nil), args...)
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected args %v", args)
	})}
	preparedState := history.Prepared{State: loaded.History, Updates: [2]history.RepoUpdate{
		{State: loaded.History.Core, PublishedExpectation: history.RefExpectation{ExpectedOID: loaded.History.Core.TipOID}},
		{State: loaded.History.Cask},
	}}
	if err = cache.ReconcilePublished(context.Background(), preparedState); err != nil {
		t.Fatal(err)
	}
	if got := seen[history.RepoCore]; len(got) == 0 || got[len(got)-1] != loaded.History.Core.TipOID {
		t.Fatal(got)
	}
	if got := seen[history.RepoCask]; len(got) == 0 || got[len(got)-1] != strings.Repeat("0", len(loaded.History.Cask.TipOID)) {
		t.Fatal(got)
	}
}

func fakeCatPackages() []domain.Package {
	return []domain.Package{{Name: "new", Kind: domain.KindFormula, FormerNames: []string{"old"}}}
}

type refreshGitRunner func(context.Context, string, ...string) ([]byte, error)

func (f refreshGitRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return f(ctx, dir, args...)
}

func TestStaleBoundary(t *testing.T) {
	a := time.Unix(1, 0)
	if Stale(a, a.Add(24*time.Hour-time.Nanosecond)) || !Stale(a, a.Add(24*time.Hour)) {
		t.Fatal()
	}
}
func TestNeedsRefreshMigrationAndHistory(t *testing.T) {
	now := time.Unix(100000, 0)
	fresh := now.Add(-time.Hour)
	complete := completePrepared().State
	cases := []struct {
		name string
		s    store.Snapshot
		want bool
	}{{"v1", store.Snapshot{SchemaVersion: 1, RefreshedAt: fresh}, true}, {"v2 incomplete", store.Snapshot{SchemaVersion: 2, RefreshedAt: fresh}, true}, {"v2 fresh", store.Snapshot{SchemaVersion: 2, RefreshedAt: fresh, HistoryLayoutVersion: store.HistoryLayoutVersion, History: complete}, false}, {"v2 stale", store.Snapshot{SchemaVersion: 2, RefreshedAt: now.Add(-24 * time.Hour), HistoryLayoutVersion: store.HistoryLayoutVersion, History: complete}, true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsRefresh(tc.s, now); got != tc.want {
				t.Fatalf("%v", got)
			}
		})
	}
}
