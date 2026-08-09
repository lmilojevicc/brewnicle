package history

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/milo/brewnicle/internal/domain"
)

func TestResolveUsesFormerName(t *testing.T) {
	at := time.Unix(1, 0)
	got := Resolve([]domain.Package{{Name: "new", Kind: domain.KindFormula, FormerNames: []string{"old"}}}, map[string]time.Time{"old": at}, nil, time.Unix(2, 0))
	if got[0].AddedAt == nil || !got[0].AddedAt.Equal(at) {
		t.Fatal(got)
	}
}
func TestLogArgsFullAndRange(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	full, e := LogArgs("repo", Revision{ToOID: b}, RepoCore)
	if e != nil {
		t.Fatal(e)
	}
	rangeArgs, e := LogArgs("repo", Revision{FromOID: a, ToOID: b}, RepoCask)
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(full, " ")
	if !strings.Contains(joined, "--full-history -c") || !strings.Contains(joined, b+" -- Formula") {
		t.Fatal(joined)
	}
	joined = strings.Join(rangeArgs, " ")
	if !strings.Contains(joined, a+".."+b+" -- Casks") {
		t.Fatal(joined)
	}
}
func TestScannerLaunchFailureNamesLogAndIsNotRecoverable(t *testing.T) {
	oid := strings.Repeat("a", 40)
	_, err := (Scanner{Git: filepath.Join(t.TempDir(), "missing-git")}).Scan(context.Background(), t.TempDir(), Revision{ToOID: oid}, RepoCore)
	if err == nil || !strings.Contains(err.Error(), "git log") || (&Cache{}).recoverable(err) {
		t.Fatal(err)
	}
}

func TestScannerEmptyIncrementalRange(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.run(nil, "commit", "--allow-empty", "-qm", "base")
	oid := fixture.oid("HEAD")
	events, e := (Scanner{Git: fixture.bin}).Scan(context.Background(), fixture.dir+"/.git", Revision{FromOID: oid, ToOID: oid}, RepoCore)
	if e != nil || len(events) != 0 {
		t.Fatal(events, e)
	}
}
