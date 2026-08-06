package store

const SchemaVersion = "1"
const HistoryLayoutVersion = 2
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
`
