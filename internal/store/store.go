package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/milo/brewnicle/internal/domain"
	_ "modernc.org/sqlite"
)

type Snapshot struct {
	Packages             []domain.Package
	RefreshedAt          time.Time
	HistoryLayoutVersion int
}

func Load(path string) (Snapshot, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return Snapshot{}, err
	}
	defer db.Close()
	var version, refreshed string
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key='schema_version'`).Scan(&version); err != nil {
		return Snapshot{}, fmt.Errorf("metadata: %w", err)
	}
	if version != SchemaVersion {
		return Snapshot{}, fmt.Errorf("unsupported schema version %q", version)
	}
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key='last_successful_refresh'`).Scan(&refreshed); err != nil {
		return Snapshot{}, fmt.Errorf("refresh metadata: %w", err)
	}
	sec, err := strconv.ParseInt(refreshed, 10, 64)
	if err != nil {
		return Snapshot{}, err
	}
	historyLayoutVersion := 0
	var rawHistoryLayoutVersion string
	err = db.QueryRow(`SELECT value FROM metadata WHERE key='history_layout_version'`).Scan(&rawHistoryLayoutVersion)
	if err != nil && err != sql.ErrNoRows {
		return Snapshot{}, fmt.Errorf("history layout metadata: %w", err)
	}
	if err == nil {
		if parsed, parseErr := strconv.Atoi(rawHistoryLayoutVersion); parseErr == nil && parsed >= 0 {
			historyLayoutVersion = parsed
		}
	}
	rows, err := db.Query(`SELECT name,kind,description,homepage,added_at,install_target,updated_at FROM packages ORDER BY added_at IS NULL, added_at DESC, name ASC, kind ASC`)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	out := []domain.Package{}
	for rows.Next() {
		var p domain.Package
		var kind string
		var added sql.NullInt64
		var updated int64
		if err = rows.Scan(&p.Name, &kind, &p.Description, &p.Homepage, &added, &p.InstallTarget, &updated); err != nil {
			return Snapshot{}, err
		}
		p.Kind = domain.Kind(kind)
		if !p.Kind.Valid() {
			return Snapshot{}, fmt.Errorf("invalid kind %q", kind)
		}
		if added.Valid {
			v := time.Unix(added.Int64, 0).UTC()
			p.AddedAt = &v
		}
		p.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return Snapshot{}, err
	}
	if len(out) == 0 {
		return Snapshot{}, fmt.Errorf("empty package index")
	}
	return Snapshot{Packages: out, RefreshedAt: time.Unix(sec, 0).UTC(), HistoryLayoutVersion: historyLayoutVersion}, nil
}
