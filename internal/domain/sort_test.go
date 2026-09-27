package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestSortPackagesModesAndTies(t *testing.T) {
	packages := []Package{
		{Name: "zeta", Kind: KindFormula, AddedAt: tm("2026-08-06T12:00:00Z")},
		{Name: "beta", Kind: KindFormula, AddedAt: tm("2026-08-01T12:00:00Z")},
		{Name: "alpha", Kind: KindFormula},
		{Name: "beta", Kind: KindCask, AddedAt: tm("2026-08-01T13:00:00+01:00")},
		{Name: "omega", Kind: KindFont},
		{Name: "aardvark", Kind: KindCask, AddedAt: tm("2026-08-01T12:00:00Z")},
	}
	for _, tc := range []struct {
		mode SortMode
		want []int
	}{
		{SortNewest, []int{0, 5, 3, 1, 2, 4}},
		{SortOldest, []int{5, 3, 1, 0, 2, 4}},
		{SortNameAZ, []int{5, 2, 3, 1, 4, 0}},
		{SortNameZA, []int{0, 4, 3, 1, 2, 5}},
		{SortMode("invalid"), []int{0, 5, 3, 1, 2, 4}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := append([]Package(nil), packages...)
			SortPackages(got, tc.mode)
			for i, index := range tc.want {
				if got[i].Key() != packages[index].Key() {
					t.Fatalf("position %d = %s, want %s", i, got[i].Key(), packages[index].Key())
				}
			}
		})
	}
}

func TestSortPackagesUndatedAndEmpty(t *testing.T) {
	for _, mode := range SortModes {
		SortPackages(nil, mode)
		SortPackages([]Package{}, mode)
		got := []Package{{Name: "z", Kind: KindFormula}, {Name: "a", Kind: KindFormula}, {Name: "a", Kind: KindCask}}
		SortPackages(got, mode)
		want := []Package{{Name: "a", Kind: KindCask}, {Name: "a", Kind: KindFormula}, {Name: "z", Kind: KindFormula}}
		if mode == SortNameZA {
			want = []Package{want[2], want[0], want[1]}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %v, want %v", mode, got, want)
		}
	}
}

func TestSortModeLabelsAndCycling(t *testing.T) {
	labels := []string{"Newest", "Oldest", "Name A–Z", "Name Z–A"}
	for i, mode := range SortModes {
		if !mode.Valid() || mode.String() != labels[i] {
			t.Fatalf("mode %q label = %s", string(mode), mode)
		}
		for _, delta := range []int{-5, -1, 0, 1, 4, 5} {
			want := SortModes[(i+delta+8)%len(SortModes)]
			if got := CycleSortMode(mode, delta); got != want {
				t.Fatalf("cycle %s by %d = %s, want %s", mode, delta, got, want)
			}
		}
	}
	invalid := SortMode("invalid")
	if invalid.Valid() || invalid.String() != "Newest" || CycleSortMode(invalid, 1) != SortOldest || CycleSortMode(invalid, -1) != SortNameZA {
		t.Fatal("invalid mode must behave as Newest")
	}
}

func TestFilterSortingDoesNotMutateSource(t *testing.T) {
	packages := []Package{{Name: "z", Kind: KindFormula}, {Name: "a", Kind: KindCask}}
	original := append([]Package(nil), packages...)
	for _, mode := range SortModes {
		filtered := Filter(packages, RangeAll, KindFilterAll, "", time.Now())
		SortPackages(filtered, mode)
		if !reflect.DeepEqual(packages, original) {
			t.Fatalf("%s changed source: %v", mode, packages)
		}
	}
}
