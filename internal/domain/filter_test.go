package domain

import (
	"reflect"
	"testing"
	"time"
)

func tm(s string) *time.Time { v, _ := time.Parse(time.RFC3339, s); return &v }

func TestFilterBoundariesSearchSortAndKind(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.FixedZone("x", 3600))
	ps := []Package{
		{Name: "same", Kind: KindFormula, Description: "needle", AddedAt: tm("2026-07-07T11:00:00Z")},
		{Name: "same", Kind: KindCask, Description: "needle", AddedAt: tm("2026-07-07T11:00:00Z")},
		{Name: "new", Kind: KindFormula, Description: "Needle tool", AddedAt: tm("2026-08-06T10:00:00Z")},
		{Name: "font-new", Kind: KindFont, Description: "Needle font", AddedAt: tm("2026-08-05T10:00:00Z")},
		{Name: "old", Kind: KindFormula, AddedAt: tm("2026-07-07T10:59:59Z")},
		{Name: "unknown", Kind: KindCask, Description: "needle"},
	}
	got := Filter(ps, Range30D, KindFilterAll, "NEEDLE", now)
	names := []string{}
	for _, p := range got {
		names = append(names, string(p.Kind)+":"+p.Name)
	}
	want := []string{"formula:new", "font:font-new", "cask:same", "formula:same"}
	cutoffUTC := now.UTC().Add(-30 * 24 * time.Hour)
	if !got[2].AddedAt.Equal(cutoffUTC) || got[2].AddedAt.Location() != time.UTC {
		t.Fatalf("UTC boundary mismatch: got %v cutoff %v", got[2].AddedAt, cutoffUTC)
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v want %v", names, want)
	}
	all := Filter(ps, RangeAll, KindFilterAll, "", now)
	if all[len(all)-1].Name != "unknown" {
		t.Fatalf("unknown not last: %#v", all)
	}

	for _, tc := range []struct {
		filter KindFilter
		want   []string
	}{
		{KindFilterFormula, []string{"new", "same"}},
		{KindFilterCask, []string{"same"}},
		{KindFilterFont, []string{"font-new"}},
		{KindFilter("invalid"), []string{"new", "font-new", "same", "same"}},
	} {
		got := Filter(ps, Range30D, tc.filter, "needle", now)
		names := make([]string, len(got))
		for i := range got {
			names[i] = got[i].Name
		}
		if !reflect.DeepEqual(names, tc.want) {
			t.Errorf("%s got %v want %v", tc.filter, names, tc.want)
		}
	}
	if got := Filter(ps, Range7D, KindFilterFont, "missing", now); len(got) != 0 {
		t.Fatalf("empty AND filter = %#v", got)
	}
}

func TestExactRangeBoundaries(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		r    Range
		days int
	}{{Range7D, 7}, {Range30D, 30}, {Range90D, 90}, {Range1Y, 365}} {
		inside := now.Add(-time.Duration(tc.days) * 24 * time.Hour)
		before := inside.Add(-time.Second)
		got := Filter([]Package{{Name: "in", Kind: KindFormula, AddedAt: &inside}, {Name: "out", Kind: KindFormula, AddedAt: &before}, {Name: "unknown", Kind: KindFormula}}, tc.r, KindFilterAll, "", now)
		if len(got) != 1 || got[0].Name != "in" {
			t.Errorf("%s: %#v", tc.r, got)
		}
	}
}

func TestCycleRangeAndKindFilter(t *testing.T) {
	if CycleRange(RangeAll, 1) != Range7D || CycleRange(Range7D, -1) != RangeAll {
		t.Fatal("range cycle")
	}
	if CycleKindFilter(KindFilterAll, 1) != KindFilterFormula || CycleKindFilter(KindFilterAll, -1) != KindFilterFont || CycleKindFilter(KindFilterFont, 1) != KindFilterAll {
		t.Fatal("kind cycle")
	}
	invalid := KindFilter("invalid")
	if invalid.Valid() || invalid.String() != "all" || CycleKindFilter(invalid, 1) != KindFilterFormula {
		t.Fatal("invalid kind filter policy")
	}
}
