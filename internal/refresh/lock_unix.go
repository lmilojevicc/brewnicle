//go:build darwin || linux

package refresh

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type ReleaseFunc func() error

type Locker interface {
	Acquire(context.Context, func()) (ReleaseFunc, error)
}

type lockOps struct {
	open  func(string, int, uint32) (int, error)
	flock func(int, int) error
	fstat func(int, *unix.Stat_t) error
	close func(int) error
	poll  time.Duration
}

type FileLock struct {
	Path string
	ops  *lockOps
}

func (l FileLock) Acquire(ctx context.Context, waiting func()) (ReleaseFunc, error) {
	o := l.realOps()
	fd, err := o.open(l.Path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	closeOnError := func(e error) (ReleaseFunc, error) { return nil, errors.Join(e, o.close(fd)) }
	var stat unix.Stat_t
	if err = o.fstat(fd, &stat); err != nil {
		return closeOnError(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(unix.Geteuid()) {
		return closeOnError(fmt.Errorf("refresh lock must be a regular file owned by the current user"))
	}
	reported := false
	for {
		err = o.flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return closeOnError(err)
		}
		if !reported && waiting != nil {
			waiting()
			reported = true
		}
		select {
		case <-ctx.Done():
			return closeOnError(ctx.Err())
		case <-time.After(o.poll):
		}
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			unlockErr := o.flock(fd, unix.LOCK_UN)
			closeErr := o.close(fd)
			releaseErr = errors.Join(unlockErr, closeErr)
		})
		return releaseErr
	}, nil
}

func (l FileLock) realOps() *lockOps {
	if l.ops != nil {
		return l.ops
	}
	return &lockOps{open: unix.Open, flock: unix.Flock, fstat: unix.Fstat, close: unix.Close, poll: 50 * time.Millisecond}
}
