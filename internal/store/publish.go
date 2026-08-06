package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/milo/brewnicle/internal/domain"
)

type Publisher struct {
	Path    string
	Rename  func(string, string) error
	SyncDir func(string) error
}
type PublishResult struct {
	Snapshot Snapshot
	Warning  error
}

func (p Publisher) Publish(ctx context.Context, packages []domain.Package, refreshed time.Time) (PublishResult, error) {
	if len(packages) == 0 {
		return PublishResult{}, fmt.Errorf("refusing to publish empty index")
	}
	dir := filepath.Dir(p.Path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return PublishResult{}, err
	}
	f, err := os.CreateTemp(dir, ".index-*.db.tmp")
	if err != nil {
		return PublishResult{}, err
	}
	tmp := f.Name()
	_ = f.Close()
	defer os.Remove(tmp)
	if err = os.Chmod(tmp, 0600); err != nil {
		return PublishResult{}, err
	}
	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return PublishResult{}, err
	}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	if _, err = db.ExecContext(ctx, schema); err != nil {
		return PublishResult{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return PublishResult{}, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO packages(name,kind,description,homepage,added_at,install_target,updated_at) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return PublishResult{}, err
	}
	for _, pkg := range packages {
		var added any
		if pkg.AddedAt != nil {
			added = pkg.AddedAt.UTC().Unix()
		}
		target := pkg.InstallTarget
		if target == "" {
			target = pkg.Name
		}
		if _, err = stmt.ExecContext(ctx, pkg.Name, string(pkg.Kind), pkg.Description, pkg.Homepage, added, target, pkg.UpdatedAt.UTC().Unix()); err != nil {
			stmt.Close()
			tx.Rollback()
			return PublishResult{}, err
		}
	}
	_ = stmt.Close()
	if _, err = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('schema_version',?),('last_successful_refresh',?),('history_layout_version',?)`, SchemaVersion, strconv.FormatInt(refreshed.UTC().Unix(), 10), strconv.Itoa(HistoryLayoutVersion)); err != nil {
		tx.Rollback()
		return PublishResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return PublishResult{}, err
	}
	if err = db.Close(); err != nil {
		return PublishResult{}, err
	}
	ok = true
	snap, err := Load(tmp)
	if err != nil {
		return PublishResult{}, fmt.Errorf("validate temporary index: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return PublishResult{}, err
	}
	rename := p.Rename
	if rename == nil {
		rename = os.Rename
	}
	if err = rename(tmp, p.Path); err != nil {
		return PublishResult{}, err
	}
	// Rename is the publication commit point. A subsequent sync problem is a warning.
	var warning error
	syncDir := p.SyncDir
	if syncDir == nil {
		syncDir = defaultSyncDir
	}
	if err = syncDir(dir); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		warning = fmt.Errorf("index published but directory sync failed: %w", err)
	}
	return PublishResult{Snapshot: snap, Warning: warning}, nil
}
func defaultSyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func PreserveInvalid(path string, now time.Time) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	base := fmt.Sprintf("%s.corrupt-%s", path, now.UTC().Format("20060102T150405Z"))
	for attempt := 0; ; attempt++ {
		dst := base
		if attempt != 0 {
			dst = fmt.Sprintf("%s-%d", base, attempt)
		}
		// Link is an atomic no-replace operation on the supported macOS/Linux
		// filesystems. Unlinking the active name only happens after the unique
		// diagnostic name exists, so earlier evidence can never be replaced.
		err := os.Link(path, dst)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err = os.Remove(path); err != nil {
			return dst, fmt.Errorf("diagnostic preserved at %s but active path could not be removed: %w", dst, err)
		}
		return dst, nil
	}
}
