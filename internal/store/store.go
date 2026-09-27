package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/lmilojevicc/brewnicle/internal/domain"
	"github.com/lmilojevicc/brewnicle/internal/history"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("index not found")

type Snapshot struct {
	SchemaVersion        int
	Packages             []domain.Package
	RefreshedAt          time.Time
	HistoryLayoutVersion int
	History              history.State
}

type Index struct{ Path string }

func (i Index) Load() (Snapshot, error) { return Load(i.Path) }

type PublishInput struct {
	Packages    []domain.Package
	History     history.State
	RefreshedAt time.Time
}

func Load(path string) (Snapshot, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, err
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return Snapshot{}, err
	}
	defer db.Close()
	var rawVersion string
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key='schema_version'`).Scan(&rawVersion); err != nil {
		return Snapshot{}, fmt.Errorf("metadata: %w", err)
	}
	version, err := strconv.Atoi(rawVersion)
	if err != nil || (version != LegacySchemaVersion && version != CurrentSchemaVersion) {
		return Snapshot{}, fmt.Errorf("unsupported schema version %q", rawVersion)
	}
	var refreshed string
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key='last_successful_refresh'`).Scan(&refreshed); err != nil {
		return Snapshot{}, fmt.Errorf("refresh metadata: %w", err)
	}
	sec, err := strconv.ParseInt(refreshed, 10, 64)
	if err != nil {
		return Snapshot{}, err
	}
	layout := 0
	if version == CurrentSchemaVersion {
		layout, err = readRequiredNonnegativeMetadata(db, "history_layout_version")
		if err != nil {
			return Snapshot{}, err
		}
		if layout != HistoryLayoutVersion {
			return Snapshot{}, fmt.Errorf("unsupported history layout version %d", layout)
		}
	} else {
		layout = readNonnegativeMetadata(db, "history_layout_version")
	}
	packages, err := loadPackages(db)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{SchemaVersion: version, Packages: packages, RefreshedAt: time.Unix(sec, 0).UTC(), HistoryLayoutVersion: layout}
	if version == CurrentSchemaVersion {
		historyState, loadErr := loadHistory(db)
		if loadErr != nil {
			return Snapshot{}, loadErr
		}
		snap.History = historyState
	}
	return snap, nil
}

func readNonnegativeMetadata(db *sql.DB, key string) int {
	v, err := readRequiredNonnegativeMetadata(db, key)
	if err != nil {
		return 0
	}
	return v
}

func readRequiredNonnegativeMetadata(db *sql.DB, key string) (int, error) {
	var raw string
	if err := db.QueryRow(`SELECT value FROM metadata WHERE key=?`, key).Scan(&raw); err != nil {
		return 0, fmt.Errorf("%s metadata: %w", key, err)
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid %s metadata %q", key, raw)
	}
	return v, nil
}

func loadPackages(db *sql.DB) ([]domain.Package, error) {
	rows, err := db.Query(`SELECT name,kind,description,homepage,added_at,install_target,updated_at FROM packages ORDER BY added_at IS NULL, added_at DESC, name ASC, kind ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Package{}
	for rows.Next() {
		var p domain.Package
		var kind string
		var added sql.NullInt64
		var updated int64
		if err = rows.Scan(&p.Name, &kind, &p.Description, &p.Homepage, &added, &p.InstallTarget, &updated); err != nil {
			return nil, err
		}
		p.Kind = domain.Kind(kind)
		if !p.Kind.Valid() {
			return nil, fmt.Errorf("invalid kind %q", kind)
		}
		if added.Valid {
			v := time.Unix(added.Int64, 0).UTC()
			p.AddedAt = &v
		}
		p.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty package index")
	}
	return out, nil
}

func loadHistory(db *sql.DB) (history.State, error) {
	var state history.State
	rows, err := db.Query(`SELECT repo,remote_url,branch_ref,tip_oid,algorithm_version,complete FROM history_repos ORDER BY repo`)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw, remote, branch, oid string
		var algorithm, complete int
		if err = rows.Scan(&raw, &remote, &branch, &oid, &algorithm, &complete); err != nil {
			return state, err
		}
		repo := history.RepoKind(raw)
		rs := history.RepoState{Repo: repo, RemoteURL: remote, BranchRef: branch, TipOID: oid, AlgorithmVersion: algorithm, Complete: complete == 1, Events: map[string]time.Time{}}
		state.SetRepo(repo, rs)
		count++
	}
	if err = rows.Err(); err != nil {
		return state, err
	}
	if count != 2 {
		return state, fmt.Errorf("history repository state must contain core and cask")
	}
	events, err := db.Query(`SELECT repo,identifier,first_added_at FROM history_events ORDER BY repo,identifier`)
	if err != nil {
		return state, err
	}
	defer events.Close()
	for events.Next() {
		var raw, id string
		var sec int64
		if err = events.Scan(&raw, &id, &sec); err != nil {
			return state, err
		}
		repo := history.RepoKind(raw)
		rs := state.Repo(repo)
		if rs.Events == nil {
			return state, fmt.Errorf("event for unknown repository")
		}
		rs.Events[id] = time.Unix(sec, 0).UTC()
		state.SetRepo(repo, rs)
	}
	if err = events.Err(); err != nil {
		return state, err
	}
	for _, repo := range []history.RepoKind{history.RepoCore, history.RepoCask} {
		if err = state.Repo(repo).StructurallyValid(); err != nil {
			return state, err
		}
	}
	return state, nil
}
