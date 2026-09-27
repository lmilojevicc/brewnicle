package refresh

import "github.com/lmilojevicc/brewnicle/internal/domain"

type Phase string

const (
	PhaseLock          Phase = "lock"
	PhaseCatalog       Phase = "catalog"
	PhaseCatalogReady  Phase = "catalog-ready"
	PhaseSnapshotReady Phase = "snapshot-ready"
	PhaseHistory       Phase = "history"
	PhasePublish       Phase = "publish"
	PhaseDone          Phase = "done"
)

type Progress struct {
	Phase    Phase
	Detail   string
	Packages []domain.Package
}
type Summary struct {
	PackageCount, SkippedFormulae, SkippedCasks int
	Warning                                     string
}
