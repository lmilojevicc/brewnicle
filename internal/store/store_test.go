package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lmilojevicc/brewnicle/internal/domain"
	"github.com/lmilojevicc/brewnicle/internal/history"
	_ "modernc.org/sqlite"
)

func packageSet() []domain.Package {
	a := time.Unix(10, 0).UTC()
	return []domain.Package{{Name: "same", Kind: domain.KindFormula, AddedAt: &a, InstallTarget: "same", UpdatedAt: a}, {Name: "same", Kind: domain.KindCask, InstallTarget: "same", UpdatedAt: a}}
}
func historySet() history.State {
	return history.State{
		Core: history.RepoState{Repo: history.RepoCore, RemoteURL: history.CoreRemote, BranchRef: "refs/heads/main", TipOID: strings.Repeat("a", 40), AlgorithmVersion: history.AlgorithmVersion, Complete: true, Events: map[string]time.Time{"same": time.Unix(10, 0).UTC(), "removed": time.Unix(5, 0).UTC()}},
		Cask: history.RepoState{Repo: history.RepoCask, RemoteURL: history.CaskRemote, BranchRef: "refs/heads/main", TipOID: strings.Repeat("b", 40), AlgorithmVersion: history.AlgorithmVersion, Complete: true, Events: map[string]time.Time{"same": time.Unix(11, 0).UTC()}},
	}
}
func publishInput(at time.Time) PublishInput {
	return PublishInput{Packages: packageSet(), History: historySet(), RefreshedAt: at}
}

func TestPublishLoadCompleteState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	at := time.Unix(20, 0).UTC()
	r, e := (Publisher{Path: path}).Publish(context.Background(), publishInput(at))
	if e != nil {
		t.Fatal(e)
	}
	if r.Warning != nil || r.Snapshot.SchemaVersion != CurrentSchemaVersion || len(r.Snapshot.Packages) != 2 || !r.Snapshot.History.CompleteCurrent() {
		t.Fatal(r)
	}
	s, e := Load(path)
	if e != nil || s.History.Core.TipOID != strings.Repeat("a", 40) || len(s.History.Core.Events) != 2 {
		t.Fatal(s, e)
	}
}

func TestLegacyV1RemainsVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	legacy := `
CREATE TABLE packages(name TEXT NOT NULL,kind TEXT NOT NULL,description TEXT NOT NULL,homepage TEXT NOT NULL,added_at INTEGER,install_target TEXT NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(kind,name));
CREATE TABLE metadata(key TEXT PRIMARY KEY NOT NULL,value TEXT NOT NULL);
INSERT INTO packages VALUES('legacy','formula','','',1,'legacy',2);
INSERT INTO metadata VALUES('schema_version','1'),('last_successful_refresh','3'),('history_layout_version','2');`
	if _, e = db.Exec(legacy); e != nil {
		t.Fatal(e)
	}
	_ = db.Close()
	s, e := Load(path)
	if e != nil || s.SchemaVersion != 1 || len(s.Packages) != 1 || s.History.CompleteCurrent() {
		t.Fatal(s, e)
	}
}

func TestNoncurrentHistoryAlgorithmRemainsDisplayable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	if _, e := (Publisher{Path: path}).Publish(context.Background(), publishInput(time.Unix(20, 0))); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`UPDATE history_repos SET algorithm_version=? WHERE repo='core'`, history.AlgorithmVersion+1)
	if closeErr := db.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		t.Fatal(e)
	}
	s, e := Load(path)
	if e != nil || len(s.Packages) != 2 || s.History.CompleteCurrent() {
		t.Fatal(s, e)
	}
}

func assertSameGeneration(t *testing.T, want, got Snapshot) {
	t.Helper()
	if want.SchemaVersion != got.SchemaVersion || want.HistoryLayoutVersion != got.HistoryLayoutVersion || !want.RefreshedAt.Equal(got.RefreshedAt) || !reflect.DeepEqual(want.Packages, got.Packages) || !reflect.DeepEqual(want.History, got.History) {
		t.Fatalf("generation changed\nwant=%#v\ngot=%#v", want, got)
	}
}

func TestPublishFailurePreservesCompleteOldGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	old := publishInput(time.Unix(20, 0))
	if _, e := (Publisher{Path: path}).Publish(context.Background(), old); e != nil {
		t.Fatal(e)
	}
	oldSnapshot, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, stage := range []publishStage{stageSchema, stagePackages, stageEvents, stageRepos, stageCommit, stageReload, stageIntegrity, stageFileSync, stageBeforeRename} {
		t.Run(string(stage), func(t *testing.T) {
			p := Publisher{Path: path, hooks: &publishHooks{failAt: func(s publishStage) error {
				if s == stage {
					return errors.New("boom")
				}
				return nil
			}}}
			next := publishInput(time.Unix(30, 0))
			next.History.Core.TipOID = strings.Repeat("c", 40)
			if _, e := p.Publish(context.Background(), next); e == nil {
				t.Fatal("want error")
			}
			got, e := Load(path)
			if e != nil {
				t.Fatal(e)
			}
			assertSameGeneration(t, oldSnapshot, got)
		})
	}
	t.Run("rename", func(t *testing.T) {
		p := Publisher{Path: path, hooks: &publishHooks{rename: func(string, string) error { return errors.New("rename") }}}
		if _, e := p.Publish(context.Background(), publishInput(time.Unix(30, 0))); e == nil {
			t.Fatal("want error")
		}
		got, e := Load(path)
		if e != nil {
			t.Fatal(e)
		}
		assertSameGeneration(t, oldSnapshot, got)
	})
}

func TestSchemaV2RequiresCurrentLayoutMetadata(t *testing.T) {
	cases := []struct {
		name, sql string
	}{
		{"missing", `DELETE FROM metadata WHERE key='history_layout_version'`},
		{"nonnumeric", `UPDATE metadata SET value='x' WHERE key='history_layout_version'`},
		{"negative", `UPDATE metadata SET value='-1' WHERE key='history_layout_version'`},
		{"wrong", `UPDATE metadata SET value='999' WHERE key='history_layout_version'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "index.db")
			if _, err := (Publisher{Path: path}).Publish(context.Background(), publishInput(time.Unix(20, 0))); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			_, execErr := db.Exec(tc.sql)
			closeErr := db.Close()
			if execErr != nil || closeErr != nil {
				t.Fatal(execErr, closeErr)
			}
			if _, err = Load(path); err == nil {
				t.Fatal("malformed schema-v2 metadata accepted")
			}
		})
	}
}

func TestDirectorySyncFailureReturnsPublishedGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	p := Publisher{Path: path, hooks: &publishHooks{syncDir: func(string) error { return errors.New("disk") }}}
	r, e := p.Publish(context.Background(), publishInput(time.Unix(20, 0)))
	if e != nil || r.Warning == nil || !r.Snapshot.History.CompleteCurrent() {
		t.Fatal(r, e)
	}
}

func TestInvalidHistoryRejected(t *testing.T) {
	in := publishInput(time.Now())
	in.History.Cask.Events = nil
	if _, e := (Publisher{Path: filepath.Join(t.TempDir(), "x")}).Publish(context.Background(), in); e == nil {
		t.Fatal("want error")
	}
}

func TestPreserveInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	if e := os.WriteFile(path, []byte("first"), 0600); e != nil {
		t.Fatal(e)
	}
	now := time.Unix(0, 0)
	first, e := PreserveInvalid(path, now)
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(path, []byte("second"), 0600)
	second, e := PreserveInvalid(path, now)
	if e != nil || first == second {
		t.Fatal(first, second, e)
	}
	for file, want := range map[string]string{first: "first", second: "second"} {
		got, readErr := os.ReadFile(file)
		if readErr != nil || string(got) != want {
			t.Fatal(file, string(got), readErr)
		}
	}
}
