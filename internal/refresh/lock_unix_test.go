//go:build darwin || linux

package refresh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileLockContentionCancelAndReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refresh.lock")
	lock := FileLock{Path: path}
	release, e := lock.Acquire(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	waited := 0
	if _, e = lock.Acquire(ctx, func() { waited++ }); e == nil || waited != 1 {
		t.Fatal(e, waited)
	}
	if e = release(); e != nil {
		t.Fatal(e)
	}
	again, e := lock.Acquire(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = again(); e != nil {
		t.Fatal(e)
	}
}
func TestFileLockCloseRunsAfterUnlockFailure(t *testing.T) {
	closed := 0
	lock := FileLock{Path: "unused", ops: &lockOps{
		open: func(string, int, uint32) (int, error) { return 9, nil },
		fstat: func(_ int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG
			stat.Uid = uint32(unix.Geteuid())
			return nil
		},
		flock: func(_ int, how int) error {
			if how == unix.LOCK_UN {
				return errors.New("unlock")
			}
			return nil
		},
		close: func(int) error { closed++; return nil }, poll: time.Millisecond,
	}}
	release, err := lock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = release(); err == nil || closed != 1 {
		t.Fatal(err, closed)
	}
}

func TestFileLockReleaseIsIdempotent(t *testing.T) {
	lock := FileLock{Path: filepath.Join(t.TempDir(), "refresh.lock")}
	release, e := lock.Acquire(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = release(); e != nil {
		t.Fatal(e)
	}
	if e = release(); e != nil {
		t.Fatal(e)
	}
}

func TestFileLockRejectsSymlinkAndWrongType(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err == nil {
		if _, err := (FileLock{Path: link}).Acquire(context.Background(), nil); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	if _, err := (FileLock{Path: dir}).Acquire(context.Background(), nil); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestFileLockAcquireFailuresCloseDescriptor(t *testing.T) {
	cases := []struct {
		name  string
		fstat func(int, *unix.Stat_t) error
		flock func(int, int) error
	}{
		{"fstat", func(int, *unix.Stat_t) error { return errors.New("stat") }, func(int, int) error { return nil }},
		{"wrong type", func(_ int, s *unix.Stat_t) error { s.Mode = unix.S_IFDIR; s.Uid = uint32(unix.Geteuid()); return nil }, func(int, int) error { return nil }},
		{"wrong owner", func(_ int, s *unix.Stat_t) error {
			s.Mode = unix.S_IFREG
			s.Uid = uint32(unix.Geteuid() + 1)
			return nil
		}, func(int, int) error { return nil }},
		{"flock", func(_ int, s *unix.Stat_t) error { s.Mode = unix.S_IFREG; s.Uid = uint32(unix.Geteuid()); return nil }, func(int, int) error { return errors.New("flock") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			closed := 0
			lock := FileLock{Path: "unused", ops: &lockOps{open: func(string, int, uint32) (int, error) { return 7, nil }, fstat: tc.fstat, flock: tc.flock, close: func(int) error { closed++; return nil }, poll: time.Millisecond}}
			if _, err := lock.Acquire(context.Background(), nil); err == nil || closed != 1 {
				t.Fatal(err, closed)
			}
		})
	}
}

func TestFileLockReleaseIncludesCloseFailure(t *testing.T) {
	lock := FileLock{Path: "unused", ops: &lockOps{
		open:  func(string, int, uint32) (int, error) { return 8, nil },
		fstat: func(_ int, s *unix.Stat_t) error { s.Mode = unix.S_IFREG; s.Uid = uint32(unix.Geteuid()); return nil },
		flock: func(int, int) error { return nil }, close: func(int) error { return errors.New("close") }, poll: time.Millisecond,
	}}
	release, err := lock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = release(); err == nil || !strings.Contains(err.Error(), "close") {
		t.Fatal(err)
	}
}

func TestFileLockWaitingHandoff(t *testing.T) {
	lock := FileLock{Path: filepath.Join(t.TempDir(), "refresh.lock")}
	first, err := lock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan ReleaseFunc, 1)
	waited := make(chan struct{}, 1)
	go func() {
		release, acquireErr := lock.Acquire(context.Background(), func() {
			select {
			case waited <- struct{}{}:
			default:
			}
		})
		if acquireErr == nil {
			acquired <- release
		}
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("wait not reported")
	}
	if err = first(); err != nil {
		t.Fatal(err)
	}
	select {
	case release := <-acquired:
		if err = release(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second waiter did not acquire")
	}
}
