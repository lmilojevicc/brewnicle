package ui

import (
	"github.com/lmilojevicc/brewnicle/internal/domain"
	"github.com/lmilojevicc/brewnicle/internal/refresh"
)

type RefreshEvent struct {
	Progress      *refresh.Progress
	Packages      []domain.Package
	Summary       refresh.Summary
	Err           error
	CatalogReady  bool
	SnapshotReady bool
	Done          bool
}
type RefreshStarter func() <-chan RefreshEvent

type startupFrameMsg struct{}
type startRefreshMsg struct{}
type indexTickMsg struct{ RunID uint64 }
type refreshEventMsg struct{ Event RefreshEvent }
type ActionResultMsg struct {
	Action string
	Err    error
}
