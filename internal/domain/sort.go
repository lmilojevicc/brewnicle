package domain

import "sort"

type SortMode string

const (
	SortNewest      SortMode = "newest"
	SortOldest      SortMode = "oldest"
	SortNameAZ      SortMode = "name-az"
	SortNameZA      SortMode = "name-za"
	DefaultSortMode          = SortNewest
)

var SortModes = []SortMode{SortNewest, SortOldest, SortNameAZ, SortNameZA}

func (mode SortMode) Valid() bool {
	switch mode {
	case SortNewest, SortOldest, SortNameAZ, SortNameZA:
		return true
	default:
		return false
	}
}

func (mode SortMode) String() string {
	switch mode {
	case SortOldest:
		return "Oldest"
	case SortNameAZ:
		return "Name A–Z"
	case SortNameZA:
		return "Name Z–A"
	default:
		return "Newest"
	}
}

func CycleSortMode(mode SortMode, delta int) SortMode {
	idx := 0
	for i, candidate := range SortModes {
		if candidate == mode {
			idx = i
			break
		}
	}
	idx = (idx + delta) % len(SortModes)
	if idx < 0 {
		idx += len(SortModes)
	}
	return SortModes[idx]
}

// SortPackages orders the supplied slice in place. Unknown dates stay last in
// both date modes; equal dates and names use deterministic name/kind ties.
// Invalid modes use Newest, matching the default filter order.
func SortPackages(packages []Package, mode SortMode) {
	sort.SliceStable(packages, func(i, j int) bool {
		a, b := packages[i], packages[j]
		if mode != SortNameAZ && mode != SortNameZA {
			if a.AddedAt == nil && b.AddedAt != nil {
				return false
			}
			if a.AddedAt != nil && b.AddedAt == nil {
				return true
			}
			if a.AddedAt != nil && b.AddedAt != nil && !a.AddedAt.Equal(*b.AddedAt) {
				if mode == SortOldest {
					return a.AddedAt.Before(*b.AddedAt)
				}
				return a.AddedAt.After(*b.AddedAt)
			}
		}
		if a.Name != b.Name {
			if mode == SortNameZA {
				return a.Name > b.Name
			}
			return a.Name < b.Name
		}
		return a.Kind < b.Kind
	})
}
