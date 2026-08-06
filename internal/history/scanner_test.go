package history

import (
	"github.com/milo/brewnicle/internal/domain"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	old := time.Unix(10, 0)
	now := time.Unix(20, 0)
	ps := []domain.Package{{Name: "new", Kind: domain.KindFormula, FormerNames: []string{"old"}}, {Name: "font-x", Kind: domain.KindFont}}
	got := Resolve(ps, map[string]time.Time{"old": old}, map[string]time.Time{}, now)
	if got[0].AddedAt == nil || !got[0].AddedAt.Equal(old) || got[1].AddedAt != nil {
		t.Fatal(got)
	}
}
