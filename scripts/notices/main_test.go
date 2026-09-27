package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNoticeNames(t *testing.T) {
	for _, name := range []string{"LICENSE", "LICENSE.txt", "LICENSE-3RD-PARTY.md", "COPYING", "NOTICE", "PATENTS", "ATTRIB", "license_mit"} {
		if !noticeName.MatchString(name) {
			t.Errorf("missing notice: %s", name)
		}
	}
	if licenseName.MatchString("PATENTS") || licenseName.MatchString("NOTICE") {
		t.Error("attributions alone must not substitute for a license file")
	}
	for _, name := range []string{"README.md", "NOTICEABLE"} {
		if noticeName.MatchString(name) {
			t.Errorf("unexpected notice: %s", name)
		}
	}
}

func TestCopyFilePreservesNotice(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "LICENSE")
	text := []byte("Copyright © Example\nPermission notice\n")
	if err := os.WriteFile(source, text, 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "output", "module", "LICENSE")
	if err := copyFile(source, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != string(text) {
		t.Fatalf("notice changed: %q, %v", got, err)
	}
}

func TestCollectRejectsExistingOutput(t *testing.T) {
	if err := collect(t.TempDir()); err == nil {
		t.Fatal("must not mix stale notices with a new collection")
	}
}
