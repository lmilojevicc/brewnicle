package domain

import (
	"sort"
	"strings"
	"time"
)

type Range string

const (
	Range7D      Range = "7d"
	Range30D     Range = "30d"
	Range90D     Range = "90d"
	Range1Y      Range = "1y"
	RangeAll     Range = "all"
	DefaultRange       = Range30D
)

var Ranges = []Range{Range7D, Range30D, Range90D, Range1Y, RangeAll}

func (r Range) Duration() (time.Duration, bool) {
	switch r {
	case Range7D:
		return 7 * 24 * time.Hour, true
	case Range30D:
		return 30 * 24 * time.Hour, true
	case Range90D:
		return 90 * 24 * time.Hour, true
	case Range1Y:
		return 365 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

func Filter(packages []Package, r Range, query string, now time.Time) []Package {
	now = now.UTC()
	q := strings.ToLower(strings.TrimSpace(query))
	d, bounded := r.Duration()
	cutoff := now.Add(-d)
	out := make([]Package, 0, len(packages))
	for _, p := range packages {
		if bounded && (p.AddedAt == nil || p.AddedAt.UTC().Before(cutoff)) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Name), q) && !strings.Contains(strings.ToLower(p.Description), q) {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.AddedAt == nil && b.AddedAt != nil {
			return false
		}
		if a.AddedAt != nil && b.AddedAt == nil {
			return true
		}
		if a.AddedAt != nil && b.AddedAt != nil && !a.AddedAt.Equal(*b.AddedAt) {
			return a.AddedAt.After(*b.AddedAt)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Kind < b.Kind
	})
	return out
}

func CycleRange(r Range, delta int) Range {
	idx := 0
	for i, candidate := range Ranges {
		if candidate == r {
			idx = i
			break
		}
	}
	idx = (idx + delta) % len(Ranges)
	if idx < 0 {
		idx += len(Ranges)
	}
	return Ranges[idx]
}
