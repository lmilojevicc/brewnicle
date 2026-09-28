package refresh

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	xdg := filepath.Join(base, "xdg")
	t.Setenv("XDG_CACHE_HOME", xdg)
	root := filepath.Join(base, "cache")
	p, err := ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{Root: root, Index: filepath.Join(root, "index.db"), Git: filepath.Join(root, "git"), Lock: filepath.Join(root, "refresh.lock")}
	if p != want {
		t.Fatalf("ResolvePaths(explicit root) = %+v, want %+v", p, want)
	}
	if _, err := os.Stat(xdg); !os.IsNotExist(err) {
		t.Fatalf("explicit root should leave XDG cache untouched: %v", err)
	}
}

func TestResolvePathsMacOSCacheHome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS cache resolution")
	}
	for _, tc := range []struct {
		name     string
		xdg      string
		absolute bool
		unset    bool
	}{
		{name: "absolute override", absolute: true},
		{name: "unset fallback", unset: true},
		{name: "empty fallback"},
		{name: "relative fallback", xdg: "relative-cache"},
		{name: "dot fallback", xdg: "."},
		{name: "parent relative fallback", xdg: "../cache"},
		{name: "unexpanded tilde fallback", xdg: "~/cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", base)
			xdg := tc.xdg
			cacheHome := filepath.Join(base, "Library", "Caches")
			if tc.absolute {
				xdg = filepath.Join(base, "xdg", "cache")
				cacheHome = xdg
			}
			t.Setenv("XDG_CACHE_HOME", xdg)
			if tc.unset {
				if err := os.Unsetenv("XDG_CACHE_HOME"); err != nil {
					t.Fatal(err)
				}
			}
			// Keep even an incorrectly accepted relative path inside the fixture.
			workingDir := filepath.Join(base, "work")
			if err := os.Mkdir(workingDir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(workingDir)
			p, err := ResolvePaths("")
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(cacheHome, "brewnicle")
			want := Paths{Root: root, Index: filepath.Join(root, "index.db"), Git: filepath.Join(root, "git"), Lock: filepath.Join(root, "refresh.lock")}
			if p != want {
				t.Fatalf("ResolvePaths() = %+v, want %+v", p, want)
			}
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				t.Fatalf("cache root was not created as a directory: %v", err)
			}
		})
	}
}

func TestResolvePathsRejectsSymlinkRoot(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "cache")
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolvePaths(root); err == nil {
		t.Fatal("accepted symlink cache root")
	}
}
