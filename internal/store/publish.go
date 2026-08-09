package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/milo/brewnicle/internal/domain"
	"github.com/milo/brewnicle/internal/history"
)

type publishStage string

const (
	stageSchema       publishStage = "schema"
	stagePackages     publishStage = "packages"
	stageEvents       publishStage = "events"
	stageRepos        publishStage = "repos"
	stageCommit       publishStage = "commit"
	stageReload       publishStage = "reload"
	stageIntegrity    publishStage = "integrity"
	stageFileSync     publishStage = "file-sync"
	stageBeforeRename publishStage = "before-rename"
)

type publishHooks struct {
	failAt   func(publishStage) error
	rename   func(string, string) error
	syncDir  func(string) error
	syncFile func(string) error
}

type Publisher struct {
	Path  string
	hooks *publishHooks
}
type PublishResult struct {
	Snapshot Snapshot
	Warning  error
}

func (p Publisher) Load() (Snapshot, error) { return Load(p.Path) }

func (p Publisher) Publish(ctx context.Context, input PublishInput) (PublishResult, error) {
	if len(input.Packages) == 0 {
		return PublishResult{}, fmt.Errorf("refusing to publish empty index")
	}
	if !input.History.CompleteCurrent() {
		return PublishResult{}, fmt.Errorf("refusing to publish incomplete history")
	}
	if input.RefreshedAt.IsZero() {
		return PublishResult{}, fmt.Errorf("missing publication timestamp")
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
	closed := false
	defer func() {
		if !closed {
			_ = db.Close()
		}
	}()
	if err = p.fail(stageSchema); err != nil {
		return PublishResult{}, err
	}
	if _, err = db.ExecContext(ctx, schema); err != nil {
		return PublishResult{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return PublishResult{}, err
	}
	rollback := func(e error) (PublishResult, error) { _ = tx.Rollback(); return PublishResult{}, e }
	pkgStmt, err := tx.PrepareContext(ctx, `INSERT INTO packages(name,kind,description,homepage,added_at,install_target,updated_at) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return rollback(err)
	}
	for _, pkg := range input.Packages {
		var added any
		if pkg.AddedAt != nil {
			added = pkg.AddedAt.UTC().Unix()
		}
		target := pkg.InstallTarget
		if target == "" {
			target = pkg.Name
		}
		if _, err = pkgStmt.ExecContext(ctx, pkg.Name, string(pkg.Kind), pkg.Description, pkg.Homepage, added, target, pkg.UpdatedAt.UTC().Unix()); err != nil {
			_ = pkgStmt.Close()
			return rollback(err)
		}
	}
	_ = pkgStmt.Close()
	if err = p.fail(stagePackages); err != nil {
		return rollback(err)
	}
	eventStmt, err := tx.PrepareContext(ctx, `INSERT INTO history_events(repo,identifier,first_added_at) VALUES(?,?,?)`)
	if err != nil {
		return rollback(err)
	}
	for _, repo := range []history.RepoKind{history.RepoCore, history.RepoCask} {
		rs := input.History.Repo(repo)
		ids := make([]string, 0, len(rs.Events))
		for id := range rs.Events {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if _, err = eventStmt.ExecContext(ctx, string(repo), id, rs.Events[id].UTC().Unix()); err != nil {
				_ = eventStmt.Close()
				return rollback(err)
			}
		}
	}
	_ = eventStmt.Close()
	if err = p.fail(stageEvents); err != nil {
		return rollback(err)
	}
	for _, repo := range []history.RepoKind{history.RepoCore, history.RepoCask} {
		rs := input.History.Repo(repo)
		if _, err = tx.ExecContext(ctx, `INSERT INTO history_repos(repo,remote_url,branch_ref,tip_oid,algorithm_version,complete) VALUES(?,?,?,?,?,1)`, string(repo), rs.RemoteURL, rs.BranchRef, rs.TipOID, rs.AlgorithmVersion); err != nil {
			return rollback(err)
		}
	}
	if err = p.fail(stageRepos); err != nil {
		return rollback(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('schema_version',?),('last_successful_refresh',?),('history_layout_version',?)`, SchemaVersion, strconv.FormatInt(input.RefreshedAt.UTC().Unix(), 10), strconv.Itoa(HistoryLayoutVersion)); err != nil {
		return rollback(err)
	}
	if err = p.fail(stageCommit); err != nil {
		return rollback(err)
	}
	if err = tx.Commit(); err != nil {
		return PublishResult{}, err
	}
	if err = db.Close(); err != nil {
		return PublishResult{}, err
	}
	closed = true
	if err = p.fail(stageReload); err != nil {
		return PublishResult{}, err
	}
	snap, err := Load(tmp)
	if err != nil {
		return PublishResult{}, fmt.Errorf("validate temporary index: %w", err)
	}
	if err = p.fail(stageIntegrity); err != nil {
		return PublishResult{}, err
	}
	if err = integrityCheck(tmp); err != nil {
		return PublishResult{}, err
	}
	if err = comparePublished(input, snap); err != nil {
		return PublishResult{}, err
	}
	if err = p.fail(stageFileSync); err != nil {
		return PublishResult{}, err
	}
	if err = p.syncFile(tmp); err != nil {
		return PublishResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return PublishResult{}, err
	}
	if err = p.fail(stageBeforeRename); err != nil {
		return PublishResult{}, err
	}
	if err = p.rename(tmp, p.Path); err != nil {
		return PublishResult{}, err
	}
	warning := p.syncDir(dir)
	if warning != nil && !errors.Is(warning, syscall.EINVAL) && !errors.Is(warning, syscall.ENOTSUP) {
		warning = fmt.Errorf("index published but directory sync failed: %w", warning)
	} else {
		warning = nil
	}
	return PublishResult{Snapshot: snap, Warning: warning}, nil
}

func (p Publisher) fail(stage publishStage) error {
	if p.hooks != nil && p.hooks.failAt != nil {
		return p.hooks.failAt(stage)
	}
	return nil
}
func (p Publisher) rename(a, b string) error {
	if p.hooks != nil && p.hooks.rename != nil {
		return p.hooks.rename(a, b)
	}
	return os.Rename(a, b)
}
func (p Publisher) syncDir(path string) error {
	if p.hooks != nil && p.hooks.syncDir != nil {
		return p.hooks.syncDir(path)
	}
	return defaultSyncDir(path)
}
func (p Publisher) syncFile(path string) error {
	if p.hooks != nil && p.hooks.syncFile != nil {
		return p.hooks.syncFile(path)
	}
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func integrityCheck(path string) error {
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return e
	}
	defer db.Close()
	var result string
	if e = db.QueryRow(`PRAGMA integrity_check`).Scan(&result); e != nil {
		return e
	}
	if result != "ok" {
		return fmt.Errorf("integrity check: %s", result)
	}
	return nil
}
func comparePublished(in PublishInput, s Snapshot) error {
	if s.SchemaVersion != CurrentSchemaVersion || s.HistoryLayoutVersion != HistoryLayoutVersion || len(s.Packages) != len(in.Packages) || s.RefreshedAt.Unix() != in.RefreshedAt.UTC().Unix() {
		return fmt.Errorf("published index validation mismatch")
	}
	wantPackages := make(map[string]domain.Package, len(in.Packages))
	for _, pkg := range in.Packages {
		wantPackages[pkg.Key()] = pkg
	}
	for _, got := range s.Packages {
		want, ok := wantPackages[got.Key()]
		wantTarget := want.InstallTarget
		if wantTarget == "" {
			wantTarget = want.Name
		}
		if !ok || want.Description != got.Description || want.Homepage != got.Homepage || wantTarget != got.InstallTarget || !sameTimePointer(want.AddedAt, got.AddedAt) || want.UpdatedAt.UTC().Unix() != got.UpdatedAt.Unix() {
			return fmt.Errorf("published package mismatch for %s", got.Name)
		}
	}
	for _, repo := range []history.RepoKind{history.RepoCore, history.RepoCask} {
		want, got := in.History.Repo(repo), s.History.Repo(repo)
		if want.TipOID != got.TipOID || want.BranchRef != got.BranchRef || want.RemoteURL != got.RemoteURL || want.AlgorithmVersion != history.AlgorithmVersion || got.AlgorithmVersion != history.AlgorithmVersion || !got.Complete || len(want.Events) != len(got.Events) {
			return fmt.Errorf("published history mismatch for %s", repo)
		}
		for id, at := range want.Events {
			if got.Events[id].Unix() != at.UTC().Unix() {
				return fmt.Errorf("published event mismatch")
			}
		}
	}
	return nil
}
func sameTimePointer(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.UTC().Unix() == b.UTC().Unix()
}
func defaultSyncDir(path string) error {
	d, e := os.Open(path)
	if e != nil {
		return e
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
