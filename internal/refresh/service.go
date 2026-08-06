package refresh

import (
	"context"
	"fmt"
	"github.com/milo/brewnicle/internal/catalog"
	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/history"
	"github.com/milo/brewnicle/internal/store"
	"time"
)

type Catalog interface {
	Fetch(context.Context) (catalog.Result, error)
}
type History interface {
	UpdateAll(context.Context, history.ProgressFunc) (map[string]time.Time, map[string]time.Time, error)
}
type Publisher interface {
	Publish(context.Context, []domain.Package, time.Time) (store.PublishResult, error)
}
type Service struct {
	Catalog   Catalog
	History   History
	Publisher Publisher
	Now       func() time.Time
}

func (s Service) Run(ctx context.Context, emit func(Progress)) (store.PublishResult, Summary, error) {
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	send := func(p Phase, d string) {
		if emit != nil {
			emit(Progress{p, d})
		}
	}
	send(PhaseCatalog, "fetching formulae and casks")
	cat, err := s.Catalog.Fetch(ctx)
	if err != nil {
		return store.PublishResult{}, Summary{}, err
	}
	if len(cat.Packages) == 0 {
		return store.PublishResult{}, Summary{}, fmt.Errorf("catalog contained no eligible packages")
	}
	if err = ctx.Err(); err != nil {
		return store.PublishResult{}, Summary{}, err
	}
	send(PhaseHistory, "updating official histories")
	core, casks, err := s.History.UpdateAll(ctx, func(d string) { send(PhaseHistory, d) })
	if err != nil {
		return store.PublishResult{}, Summary{}, err
	}
	joined := history.Resolve(cat.Packages, core, casks, now)
	if err = ctx.Err(); err != nil {
		return store.PublishResult{}, Summary{}, err
	}
	send(PhasePublish, "publishing index")
	result, err := s.Publisher.Publish(ctx, joined, now)
	if err != nil {
		return store.PublishResult{}, Summary{}, err
	}
	summary := Summary{PackageCount: len(result.Snapshot.Packages), SkippedFormulae: cat.SkippedFormulae, SkippedCasks: cat.SkippedCasks}
	if result.Warning != nil {
		summary.Warning = result.Warning.Error()
	}
	send(PhaseDone, "refresh complete")
	return result, summary, nil
}
func Stale(refreshed, now time.Time) bool {
	return !now.UTC().Before(refreshed.UTC().Add(24 * time.Hour))
}
