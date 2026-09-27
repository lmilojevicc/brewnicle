package refresh

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"sync"
	"time"

	"github.com/lmilojevicc/brewnicle/internal/catalog"
	"github.com/lmilojevicc/brewnicle/internal/domain"
	"github.com/lmilojevicc/brewnicle/internal/history"
	"github.com/lmilojevicc/brewnicle/internal/store"
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
	Catalog         Catalog
	History         History
	Index           Index
	Locker          Locker
	Now             func() time.Time
	CatalogFirst    bool            // set only when the application launched without a valid index
	VisibleSnapshot *store.Snapshot // exact generation initially displayed by the caller, if any

	runMu             sync.Mutex
	bootstrapDone     bool
	visibleGeneration [sha256.Size]byte
	visibleSet        bool
}

type noopLocker struct{}

func (noopLocker) Acquire(context.Context, func()) (ReleaseFunc, error) {
	return func() error { return nil }, nil
}

func (s *Service) Run(ctx context.Context, emit func(Progress)) (result store.PublishResult, summary Summary, err error) {
	// A Service can be reused by manual refreshes. Serialize those runs so the
	// one-shot bootstrap handoff and its emitted events remain race-free.
	s.runMu.Lock()
	defer s.runMu.Unlock()
	catalogFirst := s.CatalogFirst && !s.bootstrapDone
	if !s.visibleSet && s.VisibleSnapshot != nil {
		s.visibleGeneration = snapshotGeneration(*s.VisibleSnapshot)
		s.visibleSet = true
	}

	send := func(p Phase, d string) {
		if emit != nil {
			emit(Progress{Phase: p, Detail: d})
		}
	}
	sendCatalogReady := func(packages []domain.Package) {
		if emit != nil {
			emit(Progress{
				Phase:    PhaseCatalogReady,
				Detail:   "catalog ready; indexing exact package dates",
				Packages: append([]domain.Package(nil), packages...),
			})
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
	if loadErr == nil && completeSnapshot(previous) && len(previous.Packages) > 0 {
		generation := snapshotGeneration(previous)
		emitSnapshot := catalogFirst && !s.visibleSet
		if s.visibleSet && generation != s.visibleGeneration {
			emitSnapshot = true
		}
		if emitSnapshot && emit != nil {
			emit(Progress{
				Phase:    PhaseSnapshotReady,
				Detail:   "using package index published by another Brewnicle process",
				Packages: append([]domain.Package(nil), previous.Packages...),
			})
		}
		// Track the generation loaded under the inter-process lock even when
		// the caller omitted startup identity and no handoff was needed.
		s.visibleGeneration = generation
		s.visibleSet = true
		if catalogFirst {
			// The exact winning generation is authoritative even if the
			// subsequent catalog refresh fails.
			s.bootstrapDone = true
		}
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
	// Only a true bootstrap may expose catalog rows without exact dates. A
	// valid cached generation remains authoritative throughout stale refreshes.
	if catalogFirst && errors.Is(loadErr, store.ErrNotFound) {
		sendCatalogReady(cat.Packages)
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
	s.visibleGeneration = snapshotGeneration(result.Snapshot)
	s.visibleSet = true
	if catalogFirst {
		s.bootstrapDone = true
	}
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
	return !completeSnapshot(snapshot) || Stale(snapshot.RefreshedAt, now)
}

func completeSnapshot(snapshot store.Snapshot) bool {
	return snapshot.SchemaVersion >= store.CurrentSchemaVersion && snapshot.HistoryLayoutVersion >= store.HistoryLayoutVersion && snapshot.History.CompleteCurrent()
}

// snapshotGeneration fingerprints every persisted field that can affect the
// visible package list, plus the exact history tips backing the publication.
// Store timestamps alone are not unique when two processes publish in the same
// second, so they are insufficient for cross-process handoff identity.
func snapshotGeneration(snapshot store.Snapshot) [sha256.Size]byte {
	h := sha256.New()
	writeGenerationInt(h, int64(snapshot.SchemaVersion))
	writeGenerationInt(h, int64(snapshot.HistoryLayoutVersion))
	writeGenerationInt(h, snapshot.RefreshedAt.UnixNano())
	for _, state := range []history.RepoState{snapshot.History.Core, snapshot.History.Cask} {
		writeGenerationString(h, string(state.Repo))
		writeGenerationString(h, state.RemoteURL)
		writeGenerationString(h, state.BranchRef)
		writeGenerationString(h, state.TipOID)
		writeGenerationInt(h, int64(state.AlgorithmVersion))
		if state.Complete {
			writeGenerationInt(h, 1)
		} else {
			writeGenerationInt(h, 0)
		}
	}
	writeGenerationInt(h, int64(len(snapshot.Packages)))
	for _, p := range snapshot.Packages {
		writeGenerationString(h, p.Name)
		writeGenerationString(h, string(p.Kind))
		writeGenerationString(h, p.Description)
		writeGenerationString(h, p.Homepage)
		writeGenerationString(h, p.InstallTarget)
		if p.AddedAt == nil {
			writeGenerationInt(h, 0)
		} else {
			writeGenerationInt(h, 1)
			writeGenerationInt(h, p.AddedAt.UnixNano())
		}
		writeGenerationInt(h, p.UpdatedAt.UnixNano())
	}
	var generation [sha256.Size]byte
	copy(generation[:], h.Sum(nil))
	return generation
}

func writeGenerationString(h hash.Hash, value string) {
	writeGenerationInt(h, int64(len(value)))
	_, _ = h.Write([]byte(value))
}

func writeGenerationInt(h hash.Hash, value int64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], uint64(value))
	_, _ = h.Write(encoded[:])
}
