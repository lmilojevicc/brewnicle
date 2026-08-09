package history

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/milo/brewnicle/internal/catalog"
)

const AlgorithmVersion = 1

const (
	FetchedRef   = "refs/brewnicle/fetched"
	PublishedRef = "refs/brewnicle/published"
)

var oidRE = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

type RepoState struct {
	Repo             RepoKind
	RemoteURL        string
	BranchRef        string
	TipOID           string
	AlgorithmVersion int
	Complete         bool
	Events           map[string]time.Time
}

type State struct {
	Core RepoState
	Cask RepoState
}

type ScanMode string

const (
	ScanFull        ScanMode = "full"
	ScanIncremental ScanMode = "incremental"
	ScanUnchanged   ScanMode = "unchanged"
)

type RefExpectation struct {
	ExpectedOID string // empty means the ref must be absent; materialized after hash width is known
}

type RepoUpdate struct {
	State                RepoState
	Mode                 ScanMode
	BaseOID              string
	PublishedExpectation RefExpectation
	RecoveredRepository  bool
}

type Prepared struct {
	State   State
	Updates [2]RepoUpdate
}

func RemoteFor(repo RepoKind) string {
	if repo == RepoCore {
		return CoreRemote
	}
	if repo == RepoCask {
		return CaskRemote
	}
	return ""
}

func ValidOID(oid string) bool { return oidRE.MatchString(oid) }

func ZeroOIDFor(oid string) string {
	if !ValidOID(oid) {
		return ""
	}
	return strings.Repeat("0", len(oid))
}

func (s RepoState) StructurallyValid() error {
	return s.StructurallyValidFor(RemoteFor(s.Repo))
}

func (s RepoState) StructurallyValidFor(remote string) error {
	if s.Repo != RepoCore && s.Repo != RepoCask {
		return fmt.Errorf("invalid history repository %q", s.Repo)
	}
	if remote == "" || s.RemoteURL != remote {
		return fmt.Errorf("unexpected remote for %s", s.Repo)
	}
	if !branchRE.MatchString(s.BranchRef) || strings.Contains(s.BranchRef, "..") {
		return fmt.Errorf("invalid branch ref %q", s.BranchRef)
	}
	if !ValidOID(s.TipOID) {
		return fmt.Errorf("invalid history OID")
	}
	if !s.Complete || s.AlgorithmVersion <= 0 || len(s.Events) == 0 {
		return fmt.Errorf("incomplete history state for %s", s.Repo)
	}
	for id, at := range s.Events {
		if !catalog.ValidIdentifier(id) || at.IsZero() || at.Unix() <= 0 {
			return fmt.Errorf("invalid history event %q", id)
		}
	}
	return nil
}

func (s RepoState) Usable() bool {
	return s.StructurallyValid() == nil && s.AlgorithmVersion == AlgorithmVersion
}

func (s State) CompleteCurrent() bool {
	return s.Core.Repo == RepoCore && s.Cask.Repo == RepoCask && s.Core.Usable() && s.Cask.Usable()
}

func (s State) Repo(repo RepoKind) RepoState {
	if repo == RepoCore {
		return s.Core
	}
	if repo == RepoCask {
		return s.Cask
	}
	return RepoState{}
}

func (s *State) SetRepo(repo RepoKind, state RepoState) {
	if repo == RepoCore {
		s.Core = state
	} else if repo == RepoCask {
		s.Cask = state
	}
}

func CloneEvents(in map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(in))
	for key, value := range in {
		out[key] = value.UTC()
	}
	return out
}

func MergeEventsMin(base, delta map[string]time.Time) map[string]time.Time {
	out := CloneEvents(base)
	for key, value := range delta {
		if old, ok := out[key]; !ok || value.Before(old) {
			out[key] = value.UTC()
		}
	}
	return out
}

func CloneState(in State) State {
	out := in
	out.Core.Events = CloneEvents(in.Core.Events)
	out.Cask.Events = CloneEvents(in.Cask.Events)
	return out
}
