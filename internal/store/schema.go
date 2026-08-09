package store

const (
	LegacySchemaVersion  = 1
	CurrentSchemaVersion = 2
	SchemaVersion        = "2"
	HistoryLayoutVersion = 3
)

const schema = `
PRAGMA journal_mode=DELETE;
CREATE TABLE packages (
 name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('formula','cask','font')),
 description TEXT NOT NULL,
 homepage TEXT NOT NULL,
 added_at INTEGER,
 install_target TEXT NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(kind,name)
);
CREATE INDEX packages_added_at_idx ON packages(added_at);
CREATE INDEX packages_kind_idx ON packages(kind);
CREATE TABLE metadata(key TEXT PRIMARY KEY NOT NULL,value TEXT NOT NULL);
CREATE TABLE history_events (
 repo TEXT NOT NULL CHECK(repo IN ('core','cask')),
 identifier TEXT NOT NULL,
 first_added_at INTEGER NOT NULL,
 PRIMARY KEY(repo,identifier)
) WITHOUT ROWID;
CREATE TABLE history_repos (
 repo TEXT PRIMARY KEY CHECK(repo IN ('core','cask')),
 remote_url TEXT NOT NULL,
 branch_ref TEXT NOT NULL,
 tip_oid TEXT NOT NULL,
 algorithm_version INTEGER NOT NULL,
 complete INTEGER NOT NULL CHECK(complete=1)
) WITHOUT ROWID;
`
