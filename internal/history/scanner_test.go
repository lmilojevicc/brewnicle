package history

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmilojevicc/brewnicle/internal/domain"
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
func TestScannerLaunchFailureNamesLogAndIsRecognized(t *testing.T) {
	oid := strings.Repeat("a", 40)
	_, err := (Scanner{Git: filepath.Join(t.TempDir(), "missing-git")}).Scan(context.Background(), t.TempDir(), Revision{ToOID: oid}, RepoCore)
	if err == nil || !strings.Contains(err.Error(), "git log") || !commandLaunchFailed(err) {
		t.Fatal(err)
	}
}

func TestScannerUsesHardenedGitEnvironment(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "environment")
	script := filepath.Join(t.TempDir(), "capture-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv > \"$BREWNICLE_ENV_CAPTURE\"\nexit 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BREWNICLE_ENV_CAPTURE", capture)
	t.Setenv("GIT_OBJECT_DIRECTORY", "/tmp/attacker-objects")
	t.Setenv("GIT_SSL_NO_VERIFY", "true")
	t.Setenv("HTTPS_PROXY", "http://attacker.test")
	_, err := (Scanner{Git: script}).Scan(context.Background(), t.TempDir(), Revision{ToOID: strings.Repeat("a", 40)}, RepoCore)
	if err == nil {
		t.Fatal("capture command unexpectedly succeeded")
	}
	raw, readErr := os.ReadFile(capture)
	if readErr != nil {
		t.Fatal(readErr)
	}
	env := strings.ToUpper(string(raw))
	for _, forbidden := range []string{"GIT_OBJECT_DIRECTORY=", "GIT_SSL_NO_VERIFY=", "HTTPS_PROXY="} {
		if strings.Contains(env, forbidden) {
			t.Fatalf("streaming scanner inherited %s: %s", forbidden, raw)
		}
	}
	for _, required := range []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(env, required) {
			t.Fatalf("streaming scanner omitted %s: %s", required, raw)
		}
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
