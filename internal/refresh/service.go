package refresh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/milo/brewnicle/internal/catalog"
	"github.com/milo/brewnicle/internal/history"
	"github.com/milo/brewnicle/internal/store"
)

type Catalog interface {
	Fetch(context.Context) (catalog.Result, error)
}
type History interface {
	PrepareAll(context.Context, history.State, history.ProgressFunc) (history.Prepared, error)
	ReconcilePublished(context.Context, history.Prepared) error
}
type Index interface {
	Load() (store.Snapshot, error)
	Publish(context.Context, store.PublishInput) (store.PublishResult, error)
}
type Service struct {
	Catalog Catalog
	History History
	Index   Index
	Locker  Locker
	Now     func() time.Time
}

type noopLocker struct{}

func (noopLocker) Acquire(context.Context, func()) (ReleaseFunc, error) {
	return func() error { return nil }, nil
}

func (s Service) Run(ctx context.Context, emit func(Progress)) (result store.PublishResult, summary Summary, err error) {
	send := func(p Phase, d string) {
		if emit != nil {
			emit(Progress{p, d})
		}
	}
	locker := s.Locker
	if locker == nil {
		locker = noopLocker{}
	}
	release, err := locker.Acquire(ctx, func() { send(PhaseLock, "waiting for another Brewnicle refresh") })
	if err != nil {
		return result, summary, err
	}
	published := false
	defer func() {
		releaseErr := release()
		if releaseErr == nil {
			return
		}
		if published {
			summary.Warning = joinWarning(summary.Warning, fmt.Errorf("index published but refresh lock release failed: %w", releaseErr))
			err = nil
		} else {
			err = errors.Join(err, releaseErr)
		}
	}()
	previous, loadErr := s.Index.Load()
	if loadErr != nil && !errors.Is(loadErr, store.ErrNotFound) {
		return result, summary, loadErr
	}
	send(PhaseCatalog, "fetching formulae and casks")
	cat, err := s.Catalog.Fetch(ctx)
	if err != nil {
		return result, summary, err
	}
	if len(cat.Packages) == 0 {
		return result, summary, fmt.Errorf("catalog contained no eligible packages")
	}
	if err = ctx.Err(); err != nil {
		return result, summary, err
	}
	if previous.SchemaVersion == store.LegacySchemaVersion {
		send(PhaseHistory, "one-time full history migration using existing Git caches")
	} else {
		send(PhaseHistory, "updating official histories")
	}
	prepared, err := s.History.PrepareAll(ctx, previous.History, func(d string) { send(PhaseHistory, d) })
	if err != nil {
		return result, summary, err
	}
	if !prepared.State.CompleteCurrent() {
		return result, summary, fmt.Errorf("history preparation incomplete")
	}
	publishedAt := time.Now().UTC().Truncate(time.Second)
	if s.Now != nil {
		publishedAt = s.Now().UTC().Truncate(time.Second)
	}
	joined := history.Resolve(cat.Packages, prepared.State.Core.Events, prepared.State.Cask.Events, publishedAt)
	if err = ctx.Err(); err != nil {
		return result, summary, err
	}
	send(PhasePublish, "publishing index")
	result, err = s.Index.Publish(ctx, store.PublishInput{Packages: joined, History: prepared.State, RefreshedAt: publishedAt})
	if err != nil {
		return result, summary, err
	}
	published = true
	summary = Summary{PackageCount: len(result.Snapshot.Packages), SkippedFormulae: cat.SkippedFormulae, SkippedCasks: cat.SkippedCasks}
	if result.Warning != nil {
		summary.Warning = joinWarning(summary.Warning, result.Warning)
	}
	send(PhaseHistory, "reconciling history pins")
	if reconcileErr := s.History.ReconcilePublished(ctx, prepared); reconcileErr != nil {
		summary.Warning = joinWarning(summary.Warning, fmt.Errorf("index published but history pin reconciliation failed: %w", reconcileErr))
	}
	send(PhaseDone, "refresh complete")
	return result, summary, nil
}
func joinWarning(existing string, err error) string {
	if err == nil {
		return existing
	}
	if existing == "" {
		return err.Error()
	}
	return existing + "; " + err.Error()
}
func Stale(refreshed, now time.Time) bool {
	return !now.UTC().Before(refreshed.UTC().Add(24 * time.Hour))
}
func NeedsRefresh(snapshot store.Snapshot, now time.Time) bool {
	return snapshot.SchemaVersion < store.CurrentSchemaVersion || snapshot.HistoryLayoutVersion < store.HistoryLayoutVersion || !snapshot.History.CompleteCurrent() || Stale(snapshot.RefreshedAt, now)
}
