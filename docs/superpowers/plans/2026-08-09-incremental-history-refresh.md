# Brewnicle Incremental History Refresh — Revised Implementation Plan

**Verdict: GO after review amendments**

This plan preserves the approved product architecture:

- SQLite stores the complete per-repository earliest-add aggregate and exact published Git cursors.
- Existing schema-v1 rows remain visible while one final background full scan migrates them.
- Normal updates scan exactly `old_oid..new_oid`; unchanged tips run no history log.
- A non-ancestor/force-pushed tip replaces only that repository’s aggregate with events reachable from the current official tip.
- UI time/type/search filters never affect backend refresh scope or state.
- SQLite rename is the publication point and the active DB is authoritative; Git refs are repairable pins.

The review’s previous NO-GO is resolved below. In particular, the worker must not infer missing peeled objects from Git exit 1, must not full-scan a shallow repository, and must use explicit full-history/combined-merge traversal semantics.

## 1. Inherited invariants and verified diagnosis

Preserve:

1. Formula/cask APIs define the complete current enabled membership.
2. Git defines `added_at`: formulae use core, casks/fonts use cask; current plus API former names choose the minimum UTC committer timestamp.
3. Removed names remain absent from `packages` but remain in `history_events` so re-adds retain the earliest reachable date.
4. The active SQLite file is not edited in place. A complete sibling temp DB is validated, synced, and renamed.
5. Every pre-rename failure leaves packages, both repo cursors, both event maps, and refresh timestamp at the old generation.
6. After rename, directory-sync, ref-reconciliation, or lock-release errors are warnings; the new visible DB must not be reported as rolled back.
7. App repositories use compiled official URLs and existing cache-root/ref/object symlink containment.
8. Cached rows render before migration/refresh; only one writer modifies source; the supervisor owns Git commands.

Verified current issues:

- `internal/history/scanner.go` runs unbounded full-history logs on every refresh.
- Local evidence attributed about 353 of 373 seconds to scanning roughly 1.306 million reachable commits.
- Current `last-good` refs advance independently before DB publication and are not published-index cursors.
- Schema v1 lacks removed identifiers and cursor OIDs, so one approved full migration scan remains necessary.

## 2. Final history and store contracts

### 2.1 History state

Add `internal/history/state.go`:

```go
const AlgorithmVersion = 1

type RepoState struct {
    Repo             RepoKind
    RemoteURL        string
    BranchRef        string
    TipOID           string
    AlgorithmVersion int
    Complete         bool
    Events           map[string]time.Time
}

type State struct {
    Core RepoState
    Cask RepoState
}

type ScanMode string
const (
    ScanFull        ScanMode = "full"
    ScanIncremental ScanMode = "incremental"
    ScanUnchanged   ScanMode = "unchanged"
)

type RefExpectation struct {
    ExpectedOID string // actual old OID, or all-zero OID meaning ref must not exist
}

type RepoUpdate struct {
    State               RepoState
    Mode                ScanMode
    BaseOID             string
    PublishedExpectation RefExpectation
    RecoveredRepository bool
}

type Prepared struct {
    State   State
    Updates [2]RepoUpdate // core, then cask
}
```

Required helpers:

- `RemoteFor(RepoKind)`; `State.Repo`/`SetRepo` without arbitrary names.
- `ValidOID`: lowercase 40- or 64-character hex only.
- `ZeroOIDFor(oid)`: all-zero string matching the repository hash width for absent-ref `update-ref` CAS.
- `RepoState.StructurallyValid`: known repo, exact compiled remote, valid branch grammar, valid OID, complete flag, valid identifiers, positive event timestamps.
- `RepoState.Usable`: structural validity plus `state.AlgorithmVersion == history.AlgorithmVersion`.
- `State.CompleteCurrent`: exactly usable core and cask states.
- deep-copy and `MergeEventsMin` helpers; active snapshot maps are never mutated.

Fonts and ordinary casks continue to share `RepoCask`. No UI filter enters these contracts.

### 2.2 Store state

Extend `internal/store/store.go`:

```go
type Snapshot struct {
    SchemaVersion        int
    Packages             []domain.Package
    RefreshedAt          time.Time
    HistoryLayoutVersion int
    History              history.State
}

type PublishInput struct {
    Packages    []domain.Package
    History     history.State
    RefreshedAt time.Time
}
```

- A valid schema-v1 snapshot returns packages plus incomplete history and remains displayable.
- A schema-v2 snapshot requires structurally valid complete history tables.
- A structurally valid schema-v2 state with a non-current algorithm remains displayable but is not an incremental base; the affected repository full-rebuilds.

## 3. Schema v2 and atomic publication

Change `internal/store/schema.go`:

- legacy schema version `1`, current schema version `2`;
- bump history layout metadata once for this migration;
- retain `packages` and `metadata`;
- add:

```sql
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
  complete INTEGER NOT NULL CHECK(complete = 1)
) WITHOUT ROWID;
```

Do not add a first-commit column in this increment; current correctness needs the aggregate timestamp and repository cursor only.

### Loader

1. Read metadata schema version before selecting a loader.
2. Schema v1:
   - validate/load existing package and refresh/layout metadata;
   - return `SchemaVersion=1`, visible packages, incomplete history;
   - never preserve this valid file as corrupt.
3. Schema v2:
   - validate packages;
   - require exactly one core and cask `history_repos` row;
   - validate remote, branch, OID, complete flag, and algorithm shape;
   - validate every event repo, identifier, and positive timestamp;
   - require a nonempty aggregate for each complete repo;
   - return structurally valid state even if algorithm is not current.
4. Other schemas or malformed schema-v2 state are errors and use existing corrupt-file preservation.
5. Add a concrete `Index.Load` or `store.ErrNotFound` so the service can distinguish missing from invalid after locking.

### Publisher

The final Publisher accepts `PublishInput` and writes packages, both repo rows, all event rows, versions, and refresh time in one transaction.

- Validate input before temp creation.
- Sort event identifiers for deterministic insertion/tests.
- After commit/close, reopen via `Load`, run `PRAGMA integrity_check`, and compare package count, both exact OIDs, repo rows, event counts/values, versions, and timestamp with input.
- Sync the closed temp file before final context check and rename.
- Rename remains the logical commit point.
- Directory-sync failure returns the new snapshot with warning.

The approved simple implementation deep-copies history in memory and writes the full `O(H+P)` candidate. Do not add a sidecar or mutate the active DB.

## 4. Scanner: exact revisions and merge-complete semantics

Refactor `internal/history/scanner.go`:

```go
type Revision struct {
    FromOID string // empty for full
    ToOID   string // required
}

func LogArgs(gitDir string, rev Revision, repo RepoKind) ([]string, error)
func (Scanner) Scan(ctx context.Context, gitDir string, rev Revision, repo RepoKind) (map[string]time.Time, error)
```

### Exact Git args

For both full `new_oid` and incremental `old_oid..new_oid`, use:

```text
git --git-dir <repo> log \
  --full-history \
  -c \
  --no-renames \
  --diff-filter=A \
  --format=%x1e%ct \
  --name-only -z \
  <immutable-revision> -- Formula|Casks
```

`-c` is the selected combined merge-diff mode:

- a file created only by merge conflict resolution is emitted when absent from all parents;
- a path already present in any parent must not be treated as newly added by the merge;
- `--full-history` prevents path simplification from pruning reachable side-branch additions, including `ours` merges.

Do not substitute first-parent traversal, timestamp cutoffs, default merge diff behavior, or history simplification.

### Parser/range rules

- Validate OIDs before revision composition.
- Full revision is immutable `to_oid`; incremental revision is exactly `from_oid..to_oid`.
- Empty incremental output is a valid empty delta.
- Full scan must return at least one valid event before state can be complete.
- Preserve strict NUL framing for nonempty output; detect empty output in Scanner or add an explicit allow-empty parser mode without weakening malformed-token rejection.
- `MergeEventsMin` deep-copies old complete state and applies minimum timestamps.

### Required real-Git merge fixtures

Run every fixture against both full `newOID` and range `oldOID..newOID`:

1. **Hidden side branch:** a side branch adds a package and is merged with an `ours` result; the reachable side-branch addition must remain in aggregate.
2. **Merge-resolution-only add:** neither parent contains a path, the merge resolution creates it; combined diff must emit it.
3. **No artificial merge add:** path exists in one parent; give merge commit an artificially earlier timestamp and prove it does not lower earliest date.

Also retain re-add, path move, former-name, `Formula/lib`, and nested-font coverage.

## 5. Typed Git outcomes and reliable commit inspection

Add `CommandError` in `internal/history/git.go` with bounded output, args, underlying error, and numeric exit status. Context cancellation and process-launch errors remain distinguishable from normal Git predicate exits.

### Raw object/type protocol

Never probe cursor existence with `cat-file -e <oid>^{commit}` and never assume its missing exit is 1.

Use this exact sequence for a stored valid-shaped OID:

1. `git --git-dir <repo> cat-file -e <raw_oid>`
   - exit 0: raw object exists; continue;
   - exit 1: absent; previous cursor is unusable and selects full fallback;
   - cancellation, launch error, or any other exit: fatal unless classified as repository corruption for the one bounded recovery below.
2. `git --git-dir <repo> cat-file -t <raw_oid>`
   - exact stdout `commit\n`: it is a usable commit;
   - successful other type: unusable cursor; select full fallback;
   - nonzero after raw existence: repository corruption candidate; select the one bounded sibling recovery unless context/launch failed.
3. Immediately pin a validated commit with `update-ref PublishedRef <oid> <explicit-expected>` before fetching over `FetchedRef`.

Add a real local Git test with a valid-shaped nonexistent raw OID. It must full-fallback rather than abort. Add a wrong-type OID test and retain fatal tests for launch/cancellation.

### Ancestry predicate

`merge-base --is-ancestor old new` remains:

- 0 ancestor;
- 1 non-ancestor/full replacement;
- other exit fatal/recovery only when proven repo corruption, never silently non-ancestor.

## 6. Shallow and corrupt repository recovery

A complete aggregate must never be published from a shallow repository.

### Shallow detection

Before **every full scan**, run:

```text
git --git-dir <repo> rev-parse --is-shallow-repository
```

Parse exact `true\n` or `false\n`; other output/error is fatal or enters the bounded corruption recovery when appropriate.

If true:

1. attempt one direct official-URL `fetch --unshallow` while preserving blob-filter retry semantics and the branch-to-`FetchedRef` refspec;
2. re-run exact shallow detection;
3. scan only after it returns false;
4. if unshallow fails or remains shallow, perform the one sibling recovery clone below.

A real `file://` shallow fixture must place the oldest package add before the shallow boundary and prove the completed aggregate contains it. A nonempty truncated log is never sufficient evidence of completeness.

### One bounded sibling recovery

For each repository preparation, allow at most one sibling recovery clone when:

- shallow unshallow fails/remains shallow;
- raw object exists but type inspection indicates corrupt history;
- an incremental or full `git log` process exits nonzero after successful launch and context is still active;
- a reachable commit/tree is missing or unreadable.

Do **not** retry parser framing errors, invalid state/OIDs, network/auth failures, executable launch failure, or cancellation as corruption.

Recovery protocol:

1. Keep the canonical repo untouched.
2. Clone the compiled official branch into a sibling temporary repo using existing blob-filter-requested/fresh-retry behavior.
3. Set/resolve `FetchedRef`, verify non-shallow, and run a full immutable-OID scan with the exact merge semantics.
4. Validate complete aggregate and repo state.
5. Only then rename canonical to a unique sibling backup and recovery temp to canonical.
6. If the second rename fails, restore the canonical backup immediately; expose failure.
7. Remove backup only after successful swap; stale backup/temp cleanup is bounded to Brewnicle-owned sibling names while holding the refresh lock.
8. No recursion: a recovery clone/scan failure aborts preparation.

Use minimal package-private file-operation hooks in `internal/history/cache.go` to test swap failure/restoration. Tests must prove canonical config/refs remain unchanged when recovery clone or full scan fails before swap.

## 7. Stateless fetched/published ref protocol

Use:

```go
const (
    FetchedRef   = "refs/brewnicle/fetched"
    PublishedRef = "refs/brewnicle/published"
)

func (c *Cache) PrepareAll(ctx context.Context, previous State, progress ProgressFunc) (Prepared, error)
func (c *Cache) ReconcilePublished(ctx context.Context, prepared Prepared) error
```

`ReconcilePublished` receives `Prepared`, not only candidate state, and stores no hidden prepare lifecycle.

### Establishing explicit expected ref state

Before fetch, read `PublishedRef` explicitly.

- **Usable DB cursor and validated commit:** CAS-repair `PublishedRef` from the observed OID (or zero OID if absent) to the DB OID; record DB OID as the expected value for post-publication CAS.
- **No usable DB cursor/migration/missing/wrong-type old object:** delete any stale published ref with explicit observed-old CAS; record all-zero expected OID, meaning the ref must be absent.
- **Recovery clone:** establish inside the validated replacement repo either the usable DB OID (only if it exists there as a commit) or explicit absence; record the resulting expectation.

`RefExpectation.ExpectedOID` is always explicit: real old OID or zero OID with the new repo’s hash width. There is no “unknown” expectation.

After SQLite rename, for each repo independently:

```text
git update-ref PublishedRef <new_db_oid> <prepared_expected_oid>
```

- First migration uses all-zero expected OID and therefore requires absence.
- CAS/ref failure is warning, not rollback.
- Crash after core reconciliation but before cask is recovered next start: DB reload is authoritative, each ref is independently observed/repaired before fetch.
- `FetchedRef` pins candidate objects across this window.

Keep legacy candidate/last-good refs as compatibility pins but never as cursors and stop advancing them. Document their possible disk retention after a rewrite.

## 8. Per-repository prepare ordering

For core, then cask:

1. Re-run existing cache/repository/ref/object/commit-graph containment before Git. Extend bounded checks to fetched/published refs and locks.
2. Discover default branch from compiled official URL.
3. Observe/establish explicit PublishedRef expectation as section 7 describes, using the raw-object/type protocol.
4. Clone missing repo through sibling-temp validation, or fetch official branch into `FetchedRef`.
5. Resolve exact immutable `FetchedRef^{commit}` output and validate OID text. Peeling here is resolution, not missing-object classification.
6. If prior state is usable and new equals old: clone aggregate, no `git log`.
7. If prior is usable, old commit validated, and ancestor predicate succeeds: scan exact range and minimum-merge.
8. Otherwise ensure non-shallow and full-scan immutable new OID, replacing only that repo aggregate.
9. On qualifying corrupt-history failure, run the single sibling recovery.
10. Return new state, scan mode/base, explicit published-ref expectation, and recovery flag.

Core success followed by cask failure may advance fetched cache refs but cannot modify active SQLite state. Next run starts from DB cursors.

## 9. SQLite schema/store details

Use schema v2 tables from the prior approved plan:

```sql
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
  complete INTEGER NOT NULL CHECK(complete = 1)
) WITHOUT ROWID;
```

The loader and publisher behavior remains section 3’s contract. Full state is loaded into memory and written completely to the candidate DB; current expected event volume does not justify sidecar generation complexity.

### Minimal deterministic failure hooks

Authorize package-private hooks only—no dependency and no public production abstraction:

```go
type publishStage string
const (
    stageSchema, stagePackages, stageEvents, stageRepos,
    stageCommit, stageReload, stageIntegrity,
    stageFileSync, stageBeforeRename publishStage = ...
)

type publishHooks struct {
    failAt  func(publishStage) error
    rename  func(string,string) error
    syncDir func(string) error
    syncFile func(string) error
}
```

`Publisher` may hold an unexported `*publishHooks`; tests use `package store`. Existing public test-only rename/sync fields should be removed or routed through these private hooks in the final API.

For every injected **pre-rename** failure, compare before/after active generation:

- package keys and dates;
- core and cask cursor rows;
- complete event maps;
- schema/layout/algorithm versions;
- refresh timestamp;
- active file remains loadable.

Post-rename directory-sync failure returns new generation plus warning.

Add similarly minimal package-private cache file hooks for sibling swap, and lock syscall hooks for unlock/close failure. Do not introduce a generic filesystem interface.

## 10. Cross-process lock: supported platforms and lifecycle

Add `internal/refresh/lock_unix.go` with:

```go
//go:build darwin || linux
```

The product supports macOS and Linux; do not imply Windows support.

Add `Paths.Lock = <cache-root>/refresh.lock` and use `golang.org/x/sys/unix` directly.

```go
type ReleaseFunc func() error

type Locker interface {
    Acquire(ctx context.Context, waiting func()) (ReleaseFunc, error)
}
```

Requirements:

- Open with `O_CREAT|O_RDWR|O_CLOEXEC|O_NOFOLLOW`, `0600`.
- `Fstat`: require regular file and `stat.Uid == uint32(unix.Geteuid())`; path is fixed beneath the validated cache root.
- Attempt `LOCK_EX|LOCK_NB`.
- On `EWOULDBLOCK/EAGAIN`, report waiting once, poll using an injected package-private interval/ops hook, honor cancellation.
- On every acquire failure/cancellation, close the descriptor.
- Release is idempotence-guarded and **always** attempts `LOCK_UN` and then `close`, even when unlock fails; return `errors.Join(unlockErr, closeErr)`.
- Leave the lock file; never unlink it.
- Reject symlink/wrong type/wrong effective UID.

Service release semantics:

- before DB rename, join release error with the refresh failure;
- after DB rename, append release error to `Summary.Warning` and still return new packages/snapshot as success;
- no deferred release error may be silently dropped.

Tests:

- native contention, waiting once, handoff, cancellation, release/reacquire;
- close attempted after injected unlock failure;
- descriptor closed on every acquire error;
- post-publication unlock/close failure returns success+warning;
- symlink/wrong-type/wrong-euid rejection.

Validation must include:

```sh
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/brewnicle
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/brewnicle
```

Run lock behavior tests natively; cross-build both supported targets.

## 11. Refresh service and publication ordering

Final interfaces:

```go
type Index interface {
    Load() (store.Snapshot, error)
    Publish(context.Context, store.PublishInput) (store.PublishResult, error)
}

type History interface {
    PrepareAll(context.Context, history.State, history.ProgressFunc) (history.Prepared, error)
    ReconcilePublished(context.Context, history.Prepared) error
}

type Service struct {
    Catalog Catalog
    History History
    Index   Index
    Locker  Locker
    Now     func() time.Time
}
```

Exact ordering:

1. Acquire lock; show waiting progress if contended.
2. Reload active index **under the lock**. Missing means bootstrap; schema v1 means visible/incomplete migration; invalid means error.
3. Fetch complete current catalogs.
4. `PrepareAll` from freshly loaded history state.
5. Require complete current candidate state.
6. Capture `publishedAt=Now().UTC()` after history work, immediately before resolve/publication.
7. Resolve all catalog packages against complete aggregate.
8. Publish packages, both repo states, all events, versions, and timestamp in one DB generation.
9. Mark `published=true` immediately after successful rename result.
10. `ReconcilePublished(ctx, prepared)` while lock held; warning only after publication.
11. Release lock with lifecycle rules above; warning only after publication.
12. Return published snapshot; UI swaps package rows once and preserves current range/kind/query/selection behavior.

Progress distinguishes lock wait, one-time migration, incremental scan, unchanged no-scan, rewrite/recovery full rebuild, publication, and reconciliation. No ETA.

`NeedsRefresh` is true for schema v1, incomplete history, non-current repo algorithm, or age staleness. Valid v1 is never preserved as corrupt.

## 12. Compiling vertical checkpoints

One writer completes each checkpoint with `go test ./...` green. Temporary compatibility entry points are explicitly allowed and must be removed at cutover.

### Checkpoint 1 — Complete history vertical, with legacy service compatibility

Implement together:

- `internal/history/state.go`, state tests;
- typed Git errors;
- Revision scanner, merge args/tests, empty delta;
- raw object/type protocol;
- fetched/published refs and explicit `Prepared` expectations;
- shallow detection/unshallow and one sibling recovery;
- cache Prepare/Reconcile plus real local Git tests.

To keep current service compiling, retain a temporary `Cache.UpdateAll` compatibility method implemented through `PrepareAll(State{})` and returning only core/cask maps. The final cutover removes it. Change cache/scanner interfaces and all history callers/fakes in this checkpoint.

Acceptance:

```sh
gofmt -w internal/history
go test ./internal/history -count=1
go test ./internal/history -run 'Test(Cache|Scanner|MergeHistory)' -count=20
go test ./... -count=1
```

Stop on unresolved shallow completeness, merge fixture, missing raw object, or recovery swap behavior.

### Checkpoint 2 — Additive dual-schema store

Implement:

- history-aware Snapshot/PublishInput;
- schema-v1 loader and schema-v2 loader;
- new `PublishState` (temporary name) writing schema v2;
- package-private failure hooks and generation comparisons.

Retain the current v1 `Publish(packages,time)` compatibility method so current service/main compile. It continues to write v1 only until checkpoint 4. Final cutover removes it and promotes the v2 method to the final Publisher interface.

Acceptance:

```sh
go test ./internal/store -count=1
go test ./internal/store -run 'Test(Publish|Schema|Legacy|Failure)' -count=20
go test ./... -count=1
```

### Checkpoint 3 — Additive lock vertical

Implement lock file/path/progress primitives and native tests without changing current Service yet.

Acceptance:

```sh
go test ./internal/refresh -run TestFileLock -count=20
go test -race ./internal/refresh -run TestFileLock -count=1
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/brewnicle
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/brewnicle
go test ./... -count=1
```

### Checkpoint 4 — Single atomic application cutover

In one compiling change:

- switch Service interfaces/order to Index + Prepare/Reconcile + Locker;
- switch concrete main wiring and every fake/test caller;
- switch Publisher to final `Publish(PublishInput)`;
- remove legacy `Cache.UpdateAll` and v1 Publisher compatibility method;
- update `NeedsRefresh`, startup migration, progress, and warning lifecycle;
- add focused UI successful-replacement state test if current coverage is insufficient.

Do not stop midway while `go test ./...` is expected to compile. Acceptance only after all dependents are updated:

```sh
go test ./internal/refresh ./cmd/brewnicle ./internal/ui -count=1
go test -race ./internal/history ./internal/store ./internal/refresh ./cmd/brewnicle -count=1
go test ./... -count=1
```

### Checkpoint 5 — Failure/recovery matrix, docs, final validation

Add remaining deterministic failures, v1-blocked-migration visibility, restart/ref repair, benchmark if deterministic, and docs.

Update:

- `README.md`;
- `docs/superpowers/specs/2026-07-26-brewnicle-design.md`;
- `docs/superpowers/plans/2026-08-06-brewnicle-implementation.md`;
- `go.mod/go.sum` to make `x/sys` direct.

Acceptance:

```sh
gofmt -w $(find cmd internal -name '*.go')
go mod tidy
go mod verify
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o /tmp/brewnicle-incremental ./cmd/brewnicle
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/brewnicle-incremental-darwin ./cmd/brewnicle
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/brewnicle-incremental-linux ./cmd/brewnicle
go test -tags=integration ./internal/catalog ./internal/history -count=1
```

The supervisor performs staged `git diff --cached --check`; writer does not stage/commit.

## 13. Focused test matrix

### Scanner/history

- full vs exact range argv includes `--full-history -c`;
- full/range hidden side branch, merge-resolution-only add, no artificial merge add;
- empty incremental range;
- minimum merge, re-add, path move, former names;
- valid-shaped missing raw OID => full fallback, not abort;
- wrong object type => full fallback;
- shallow oldest add retained after unshallow/recovery;
- corrupt incremental/full history => exactly one sibling recovery;
- recovery scan failure leaves canonical untouched.

### Git/ref recovery

- same tip calls no scanner;
- ancestor exact delta;
- rewrite replaces aggregate;
- missing old object/full fallback;
- explicit expected old and zero-absent CAS;
- restart after core-only reconciliation repairs cask from DB;
- fetched-ahead failed publication still ranges from DB cursor;
- official URL and existing containment tests remain green.

### Store/atomicity

- schema-v1 visible/incomplete;
- schema-v2 exact state round trip;
- non-current algorithm displayable but unusable;
- invalid repo/event/OID state rejected;
- package-private failpoint at every pre-rename stage compares the complete old generation;
- directory-sync warning returns complete new generation.

### Service/lock/startup

- lock acquired before reload; second process observes first publication;
- lock release/close on every error;
- post-publication ref and release errors are warnings;
- core/cask/pre-publish failures leave DB unchanged;
- blocked `PrepareAll` migration test proves already loaded v1 rows remain visible and on-disk schema remains v1 until rename;
- successful migration swaps once; next same-tip refresh performs no log;
- publication timestamp captured after history preparation.

### Filter independence — reduced scope

Do not run a combinatorial backend filter matrix.

- Assert Service/History/Index interfaces contain no range, kind, query, or selected-package parameters.
- Keep/add one focused UI test: successful package replacement preserves active range, active kind, query, and selected key when present, with existing fallback behavior when absent.

This proves decoupling without unnecessary combinations.

## 14. Minimal hooks for deterministic failure tests

Allowed package-private hooks only:

- `store.publishHooks`: stage failure, rename, file sync, directory sync, candidate reload/integrity if necessary;
- `history.cacheOps`: rename/remove/sync for validated recovery swap;
- `refresh.lockOps`: open/flock/fstat/unlock/close/poll interval;
- fake service interfaces already cover catalog/history/index failures.

No public test API, generic filesystem abstraction, failpoint dependency, or production feature flag.

Every hook defaults directly to the real operation and must be nil/zero-cost in production.

## 15. Documentation amendments

README/spec/plan must state:

- one final migration full scan reuses existing repos and old rows stay visible;
- after migration, same-tip runs no history log and normal updates scan only new reachable commits;
- scanner uses full reachable history with combined merge semantics;
- missing/shallow/corrupt/non-ancestor state triggers bounded per-repo recovery/full rebuild;
- SQLite cursors are authoritative; fetched/published refs are repairable pins;
- another process shows waiting progress under the cache lock;
- post-publication ref/lock/sync warnings do not undo the new visible generation;
- UI filters do not alter backend state;
- legacy refs may retain orphaned objects until a later cleanup change.

Do not promise an exact migration time, shallow permanent cache, or verified blob-filter effectiveness.

## 16. Stop rules

Stop and escalate rather than pivot if implementation would:

- retain orphaned force-push events (“ever observed” semantics);
- seed a complete aggregate from current package rows;
- publish from a shallow history;
- use legacy last-good as DB cursor;
- update active DB in place;
- limit backend state by UI filters;
- add a sidecar/hosted index;
- weaken strict Git framing/path validation;
- report a post-rename warning as rollback.

Do not run a real full Homebrew migration during automated validation. Use local fixture repositories; safe integration checks endpoints and remote HEAD only.

## 17. Revised review findings

Resolved mandatory findings:

- **blocker resolved:** missing-object detection uses raw `cat-file -e` plus exact `cat-file -t commit`, with real missing-OID fallback test.
- **blocker resolved:** every full scan proves non-shallow; failed unshallow/corrupt scan gets one validated sibling recovery, preserving canonical until full scan validates.
- **high resolved:** scanner uses `--full-history -c` and tests side branches, merge-only additions, and no artificial merge additions for full and range scans.
- **high resolved:** checkpoints are vertically compiling through explicit temporary compatibility methods removed at atomic cutover.
- **medium resolved:** `ReconcilePublished` receives explicit `Prepared` ref expectations; absent ref uses zero OID.
- **medium resolved:** lock has darwin/linux constraint, euid validation, guaranteed unlock+close, post-publication warning semantics, and both cross-builds.
- **medium resolved:** minimal package-private hooks make every atomicity failure deterministic.
- **low resolved:** filter testing is reduced to backend-interface decoupling plus focused UI state preservation.

No product/architecture semantics changed.

## 18. Residual risks

- One approved complete migration remains expensive once.
- SQLite and Git refs cannot share one transaction; DB authority and restart repair are the explicit recovery mechanism.
- Directory sync after rename can only be reported as a durability warning.
- Same-user filesystem/repository TOCTOU remains within the existing bounded portable checks.
- Former-name completeness remains dependent on upstream API data.
- Legacy compatibility refs may retain orphaned objects and disk usage after a rewrite.
- In-place unshallow adds history to the cache before candidate publication, but cannot make the active DB claim incomplete state; sibling recovery remains bounded.

## 19. Concise worker amendment summary

1. Use raw object existence + exact commit type; never peeled-expression exit assumptions.
2. Prove non-shallow before full scan; one validated sibling recovery for corrupt history.
3. Add `--full-history -c` and six merge fixture runs (three scenarios × full/range).
4. Keep every checkpoint compiling via temporary compatibility methods, removed in one cutover.
5. Pass `Prepared` into stateless ref reconciliation with explicit old/zero expectations.
6. Implement darwin/linux euid-owned flock and always unlock then close; post-publish failures warn.
7. Use only package-private targeted fail hooks and compare complete generations.
8. Test backend filter independence at interfaces plus one focused UI replacement-state case.

**Final verdict: GO.**

```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "Revised implementation plan resolves every severity-ranked review finding with exact target files, Git commands, interfaces, recovery ordering, vertical checkpoints, deterministic test seams, validation commands, and residual risks."
    }
  ],
  "changedFiles": [
    "/tmp/brewnicle-incremental-plan.md"
  ],
  "testsAddedOrUpdated": [],
  "commandsRun": [
    {
      "command": "read /tmp/brewnicle-incremental-plan-review.md, prior plan, and current relevant source interfaces",
      "result": "passed",
      "summary": "All mandatory corrections were traced to concrete plan sections and current target files before overwrite."
    },
    {
      "command": "git status --short --branch",
      "result": "passed",
      "summary": "Project/source worktree remained clean; only the /tmp plan artifact was overwritten."
    }
  ],
  "validationOutput": [
    "GO: reliable raw object/type fallback specified.",
    "GO: shallow/corrupt bounded recovery and canonical preservation specified.",
    "GO: full-history combined-merge semantics and fixtures specified.",
    "GO: checkpoints compile vertically through explicit temporary compatibility paths.",
    "GO: stateless ref reconciliation, portable lock lifecycle, and deterministic fail hooks specified."
  ],
  "residualRisks": [
    "One complete migration scan remains expensive.",
    "SQLite/ref cross-resource recovery relies on DB authority and idempotent repair.",
    "Same-user filesystem TOCTOU and upstream alias completeness remain bounded external risks.",
    "Legacy refs may retain orphaned objects until later cleanup."
  ],
  "noStagedFiles": true,
  "diffSummary": "Overwrote only the /tmp plan artifact; no project/source files modified.",
  "reviewFindings": [
    "blocker resolved: raw cat-file existence plus exact commit type replaces peeled-expression exit assumption.",
    "blocker resolved: shallow detection and one bounded validated repository recovery prevent incomplete aggregates.",
    "high resolved: --full-history -c and full/range merge fixtures cover reachable additions correctly.",
    "high resolved: vertical checkpoints remain compilable.",
    "medium resolved: explicit Prepared ref expectations, darwin/linux lock lifecycle, and package-private failure hooks.",
    "no remaining blockers"
  ],
  "manualNotes": "Worker should follow the eight-item amendment summary and five compiling checkpoints; supervisor owns Git operations."
}
```
