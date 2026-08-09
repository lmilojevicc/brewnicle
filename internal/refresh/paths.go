package refresh

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct{ Root, Index, Git, Lock string }

func ResolvePaths(root string) (Paths, error) {
	var err error
	if root == "" {
		root, err = os.UserCacheDir()
		if err != nil {
			return Paths{}, err
		}
		// The platform cache directory is the trusted boundary. Canonicalize it
		// once, then create only Brewnicle-owned real directories beneath it.
		if err = os.MkdirAll(root, 0700); err != nil {
			return Paths{}, err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return Paths{}, err
		}
		root = filepath.Join(root, "brewnicle")
	} else {
		root = filepath.Clean(root)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, err
	}
	if abs == string(filepath.Separator) {
		return Paths{}, fmt.Errorf("unsafe cache root")
	}
	if err = makeCacheRoot(abs); err != nil {
		return Paths{}, err
	}
	return Paths{Root: abs, Index: filepath.Join(abs, "index.db"), Git: filepath.Join(abs, "git"), Lock: filepath.Join(abs, "refresh.lock")}, nil
}

// makeCacheRoot refuses the caller-controlled final path and its immediate
// configured parent when either is a symlink. History cache descendants apply
// the stronger component-by-component boundary check before every Git command.
func makeCacheRoot(root string) error {
	parent := filepath.Dir(root)
	if info, err := os.Lstat(parent); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("cache parent must be a real directory")
	}
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("cache root must be a real directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Mkdir(root, 0700)
}
