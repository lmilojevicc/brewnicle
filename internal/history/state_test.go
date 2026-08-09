package history

import (
	"strings"
	"testing"
	"time"
)

func TestOIDAndZeroWidth(t *testing.T) {
	for _, n := range []int{40, 64} {
		oid := strings.Repeat("a", n)
		if !ValidOID(oid) || ZeroOIDFor(oid) != strings.Repeat("0", n) {
			t.Fatal(n)
		}
	}
	for _, bad := range []string{"", strings.Repeat("A", 40), strings.Repeat("a", 41)} {
		if ValidOID(bad) || ZeroOIDFor(bad) != "" {
			t.Fatal(bad)
		}
	}
}
func TestMergeEventsMinDoesNotMutateBase(t *testing.T) {
	base := map[string]time.Time{"x": time.Unix(20, 0)}
	got := MergeEventsMin(base, map[string]time.Time{"x": time.Unix(10, 0), "y": time.Unix(30, 0)})
	if got["x"].Unix() != 10 || base["x"].Unix() != 20 || got["y"].Unix() != 30 {
		t.Fatal(got, base)
	}
}
func TestRepoStateUsability(t *testing.T) {
	s := RepoState{Repo: RepoCore, RemoteURL: CoreRemote, BranchRef: "refs/heads/main", TipOID: strings.Repeat("a", 40), AlgorithmVersion: AlgorithmVersion, Complete: true, Events: map[string]time.Time{"x": time.Unix(1, 0)}}
	if !s.Usable() {
		t.Fatal()
	}
	s.AlgorithmVersion++
	if s.Usable() {
		t.Fatal()
	}
}
