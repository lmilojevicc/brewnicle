package refresh

import (
	"context"
	"errors"
	"github.com/milo/brewnicle/internal/catalog"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/history"
	"github.com/milo/brewnicle/internal/store"
	"testing"
	"time"
)

type fakeCat struct{ err error }

func (f fakeCat) Fetch(context.Context) (catalog.Result, error) {
	return catalog.Result{Packages: []domain.Package{{Name: "new", Kind: domain.KindFormula, FormerNames: []string{"old"}}}, SkippedFormulae: 2}, f.err
}

type fakeHist struct {
	called *bool
	err    error
}

func (f fakeHist) UpdateAll(context.Context, history.ProgressFunc) (map[string]time.Time, map[string]time.Time, error) {
	*f.called = true
	return map[string]time.Time{"old": time.Unix(1, 0)}, map[string]time.Time{}, f.err
}

type fakePub struct{ called *bool }

func (f fakePub) Publish(_ context.Context, p []domain.Package, n time.Time) (store.PublishResult, error) {
	*f.called = true
	return store.PublishResult{Snapshot: store.Snapshot{Packages: p, RefreshedAt: n}}, nil
}
func TestService(t *testing.T) {
	h, p := false, false
	s := Service{Catalog: fakeCat{}, History: fakeHist{&h, nil}, Publisher: fakePub{&p}, Now: func() time.Time { return time.Unix(2, 0) }}
	r, sum, e := s.Run(context.Background(), nil)
	if e != nil || !h || !p || sum.SkippedFormulae != 2 || r.Snapshot.Packages[0].AddedAt == nil {
		t.Fatal(r, sum, e)
	}
}
func TestFailureStopsPipeline(t *testing.T) {
	h, p := false, false
	s := Service{Catalog: fakeCat{errors.New("no")}, History: fakeHist{&h, nil}, Publisher: fakePub{&p}}
	if _, _, e := s.Run(context.Background(), nil); e == nil || h || p {
		t.Fatal(e, h, p)
	}
}
func TestStaleBoundary(t *testing.T) {
	a := time.Unix(1, 0)
	if Stale(a, a.Add(24*time.Hour-time.Nanosecond)) || !Stale(a, a.Add(24*time.Hour)) {
		t.Fatal()
	}
}

func TestNeedsRefreshIncludesHistoryLayoutVersion(t *testing.T) {
	now := time.Unix(100000, 0)
	fresh := now.Add(-time.Hour)
	for _, tc := range []struct {
		name    string
		version int
		age     time.Time
		want    bool
	}{
		{"legacy missing", 0, fresh, true},
		{"old", store.HistoryLayoutVersion - 1, fresh, true},
		{"current fresh", store.HistoryLayoutVersion, fresh, false},
		{"future fresh", store.HistoryLayoutVersion + 1, fresh, false},
		{"current stale", store.HistoryLayoutVersion, now.Add(-24 * time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := store.Snapshot{RefreshedAt: tc.age, HistoryLayoutVersion: tc.version}
			if got := NeedsRefresh(snapshot, now); got != tc.want {
				t.Fatalf("NeedsRefresh()=%v want %v", got, tc.want)
			}
		})
	}
}
