package refresh

type Phase string

const (
	PhaseLock    Phase = "lock"
	PhaseCatalog Phase = "catalog"
	PhaseHistory Phase = "history"
	PhasePublish Phase = "publish"
	PhaseDone    Phase = "done"
)

type Progress struct {
	Phase  Phase
	Detail string
}
type Summary struct {
	PackageCount, SkippedFormulae, SkippedCasks int
	Warning                                     string
}
