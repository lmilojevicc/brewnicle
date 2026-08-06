package store

import (
	"context"
	"errors"
	"github.com/milo/brewnicle/internal/domain"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func packageSet() []domain.Package {
	a := time.Unix(10, 0)
	return []domain.Package{{Name: "same", Kind: domain.KindFormula, AddedAt: &a, InstallTarget: "same", UpdatedAt: a}, {Name: "same", Kind: domain.KindCask, InstallTarget: "same", UpdatedAt: a}}
}
func TestPublishLoadAndCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	r, e := (Publisher{Path: path}).Publish(context.Background(), packageSet(), time.Unix(20, 0))
	if e != nil {
		t.Fatal(e)
	}
	if r.Warning != nil || len(r.Snapshot.Packages) != 2 {
		t.Fatal(r)
	}
	s, e := Load(path)
	if e != nil || len(s.Packages) != 2 || s.Packages[1].AddedAt != nil {
		t.Fatal(s, e)
	}
}
func TestRenameFailurePreservesActive(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "index.db")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	_, e := (Publisher{Path: path, Rename: func(string, string) error { return errors.New("no") }}).Publish(context.Background(), packageSet(), time.Now())
	if e == nil {
		t.Fatal("want error")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "old" {
		t.Fatalf("active changed: %q", b)
	}
}
func TestSyncFailureReturnsNewRowsAndWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	r, e := (Publisher{Path: path, SyncDir: func(string) error { return errors.New("disk") }}).Publish(context.Background(), packageSet(), time.Unix(20, 0))
	if e != nil || r.Warning == nil || len(r.Snapshot.Packages) != 2 {
		t.Fatalf("r=%+v e=%v", r, e)
	}
	if _, e = Load(path); e != nil {
		t.Fatal(e)
	}
}
func TestPreserveInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, 0)
	first, err := PreserveInvalid(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := PreserveInvalid(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("preservation destinations collided: %s", first)
	}
	for file, want := range map[string]string{first: "first", second: "second"} {
		got, readErr := os.ReadFile(file)
		if readErr != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", file, got, readErr, want)
		}
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active path remains: %v", err)
	}
}
