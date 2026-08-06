//go:build integration

package history

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOfficialRemotesExposeDefaultBranch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	g := ExecGit{}
	for _, remote := range []string{CoreRemote, CaskRemote} {
		out, e := g.Run(ctx, "", "ls-remote", "--symref", remote, "HEAD")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = parseDefaultBranch(strings.TrimSpace(string(out)) + "\n"); e != nil {
			t.Fatal(e)
		}
	}
}
