# Brewnicle Design Specification

**Date:** 2026-07-26
**Status:** Approved for implementation
**Go module:** `github.com/milo/brewnicle`

## 1. Summary

Brewnicle is a Go terminal application for discovering packages recently added to the official Homebrew catalog. It presents current formulae, casks, and font casks in a Charm split-pane TUI with package descriptions, upstream addition dates, time-range filters, search, homepage opening, and confirmed installation.

Homebrew's own `brew update` report cannot supply the required history. In install-from-API mode it compares newline-delimited snapshots such as `$(brew --cache)/api/formula_names.before.txt` with `formula_names.txt`, and the equivalent cask files, to produce the `New Formulae` and `New Casks` sections. Those files retain only the delta from the previous local update. The formula and cask API catalogs provide names, descriptions, and homepages, but no package-added timestamp. Homebrew has no durable local database of everything it has ever advertised as new.

Brewnicle therefore joins the current official API catalog with first-add events derived from the upstream git histories. It displays only non-disabled packages still available in the current catalog; it does not retain removed or disabled packages.

## 2. Goals

1. Show every non-disabled official Homebrew formula and cask in the current catalog, including fonts.
2. Date each package by its earliest known addition to the relevant upstream Homebrew git history.
3. Filter packages added within the last 7, 30, 90, or 365 days, or show every package in the non-disabled current index.
4. Search package names and descriptions interactively.
5. Show a selected package's description, exact addition date, homepage, and install command.
6. Open a selected package's homepage.
7. Install a selected package only after explicit confirmation.
8. Remain immediately useful from a valid local index when offline or when refresh fails.
9. Preserve the user's terminal palette rather than imposing an application color theme.

## 3. Non-goals

The first release does not include:

- deleted, disabled, or otherwise unavailable historical packages;
- third-party taps;
- favorites, bookmarks, ratings, or notes;
- dependency or reverse-dependency views;
- installed-only or upgrade-management views;
- uninstall or upgrade actions;
- telemetry or analytics;
- a hosted/prebuilt Brewnicle package index;
- CI, release automation, package-manager distribution, or self-update behavior;
- Windows support.

## 4. Package Scope and Semantics

### 4.1 Current non-disabled catalog only

The current formula and cask API responses are authoritative for membership, subject to excluding disabled entries. A record absent from the current catalog or marked with the API boolean field `disabled: true` is absent from Brewnicle even if it exists in git history. Records with `disabled: false` or an omitted `disabled` field remain eligible. This makes every displayed package currently installable as far as catalog status indicates and prevents stale history from appearing as installable software. The `all` count is therefore every enabled entry in the current official responses and varies as Homebrew changes; it is not the raw count including disabled entries.

### 4.2 Package kinds

Brewnicle exposes three kinds:

- `formula`: entries from the formula API;
- `cask`: non-font entries from the cask API;
- `font`: cask entries whose current token begins with `font-`.

A font is stored once with kind `font`; it is not duplicated as a cask. Fonts use cask installation semantics. Font dates use only history reachable from `Homebrew/homebrew-cask`. If a font's pre-migration addition is not reachable there, its date remains unknown; Brewnicle does not guess or require a separate historical fonts repository.

### 4.3 Addition date

`added_at` is the earliest known upstream commit's committer timestamp at which the package, or one of its known former names, was added in an applicable official repository. Timestamps are normalized to UTC.

Rules:

- Formulae are resolved against `Homebrew/homebrew-core`.
- Casks and fonts are resolved against history reachable from `Homebrew/homebrew-cask`.
- If a font's pre-migration history is not reachable from that repository, `added_at` is unknown and the font appears only in `all`.
- Former formula names and cask tokens supplied by the current APIs are included in the lookup; the earliest match across the current and former names wins.
- A delete followed by a re-add keeps the earliest observed addition date.
- Path-bucketing moves, such as moving a file into a letter directory while retaining its basename, do not reset the date.
- If no history match can be resolved, `added_at` is unknown rather than guessed.

### 4.4 Time filters and sort order

Time filters are evaluated relative to the current time in UTC:

| Filter | Inclusion rule |
|---|---|
| `7d` | `added_at >= now - 7×24h` |
| `30d` | `added_at >= now - 30×24h` |
| `90d` | `added_at >= now - 90×24h` |
| `1y` | `added_at >= now - 365×24h` |
| `all` | every package in the current non-disabled index, including unknown dates |

Unknown dates are excluded from every bounded filter. Results are sorted by `added_at` descending, then package name ascending. Unknown dates sort after all dated records in the `all` view.

The initial range is `30d` when exact dates are available. During a catalog-first bootstrap, the range is forced to `all` and bounded ranges are disabled until exact date indexing completes. Range changes are session state only and are not persisted between runs.

A separate package-type filter has `all`, `formula`, `cask`, and `font` states and defaults to `all` on every start. `f` cycles forward through those states and `F` cycles backward. Type changes are session state only and are not persisted.

Search is a case-insensitive substring match over package name and description. Search, the active time range, and the active package type are combined with logical AND.

## 5. User Experience

### 5.1 Main layout

The selected wide-terminal design is a split pane.

**Header**

- application name;
- time tabs: `7d`, `30d`, `90d`, `1y`, `all`;
- active package type: `all`, `formula`, `cask`, or `font`;
- active search query, when present;
- visible and total package counts;
- stale or refreshing indicator when applicable.

**Left pane**

- scrollable package list from the current non-disabled index;
- package name and compact `formula`, `cask`, or `font` badge;
- newest-first ordering defined in section 4.4;
- selection retained across filter changes when the selected record remains visible, otherwise moved to the first visible record;
- the selected row vertically centered when possible, using the actual list capacity after header/footer layout. Odd capacities use the exact middle; even capacities use the lower middle. The first and last pages clamp to a full page without blank padding.

**Right pane**

- package name and kind;
- exact upstream addition date and relative age, or `Date unknown`;
- wrapped description;
- homepage URL;
- exact install command;
- install availability and the latest action result.

**Footer**

- context-sensitive key hints;
- concise transient success or error status.

### 5.2 Keyboard controls

| Key | Action |
|---|---|
| `up` / `down`, `k` / `j` | Move selection |
| `1` / `2` / `3` / `4` / `5` | Select `7d` / `30d` / `90d` / `1y` / `all` |
| `tab` / `shift+tab` | Cycle time ranges forward/backward |
| `f` / `F` | Cycle package types forward/backward through `all`, `formula`, `cask`, `font` |
| `/` | Enter search input |
| `esc` | Close the active modal, leave search input, or clear an existing search |
| `o` | Open the selected homepage |
| `i` | Open install confirmation for the selected package |
| `r` | Force a catalog/history refresh |
| `?` | Toggle help overlay |
| `q`, `ctrl+c` | Quit when no text input or confirmation owns the key |
| `enter` | Confirm a focused modal action; on narrow terminals, toggle details |

### 5.3 Search

Pressing `/` focuses a one-line search field. Results update on each edit. `enter` accepts the query and returns focus to the package list. `esc` first leaves input mode while preserving a non-empty query; a subsequent `esc` clears it. The visible count updates with the query.

### 5.4 Homepage action

Pressing `o` opens the selected `http` or `https` homepage with:

- `open <url>` on macOS;
- `xdg-open <url>` on Linux.

The command is executed directly with an argument vector, not through a shell. A missing or invalid homepage is normalized to empty during catalog loading and disables `o` for that record; it does not remove the package. An unsupported operating system, missing opener, or opener failure is reported without leaving the TUI in a broken state.

### 5.5 Installation action

Pressing `i` opens a confirmation modal that displays the exact command:

- formula: `brew install <name>`;
- cask or font: `brew install --cask <token>`.

Only an exact package identifier from the loaded index can be passed to `brew`. The command is constructed as an executable and argument array and is never interpolated into a shell string.

After confirmation, Bubble Tea releases the terminal and executes Homebrew with attached stdin, stdout, and stderr so Homebrew prompts and long-running progress remain usable. When the process exits, the TUI resumes and displays success or the non-zero exit result. Canceling the confirmation runs nothing. Installation never mutates the package index.

If `brew` is unavailable, browsing and homepage opening continue to work, but installation is disabled with a clear message.

### 5.6 Responsive behavior

- At 90 or more columns and 16 or more rows, render the split-pane layout.
- From 50 through 89 columns with at least 12 rows, render the list as the primary single pane and use `enter` to toggle a full-width detail view for the selected package.
- Below 50 columns or 12 rows, render only an explicit terminal-too-small message and the quit hint.
- All widths, heights, and wrapping are calculated from Bubble Tea window-size messages; content must not write beyond the terminal bounds.
- List viewport centering is derived from the current selection and actual body height after every movement, filter, search, refresh, or resize; no independent scroll offset is persisted.

These breakpoint values belong to the UI package as named constants and are fixed by rendering tests rather than user configuration.

### 5.7 Terminal-native styling

Brewnicle uses a vivid theme made exclusively from terminal ANSI palette slots `1` through `8`; the user's terminal theme controls the actual RGB values. The application never uses RGB, ANSI-256, adaptive colors, or an explicit background.

- Title and selected package/detail names use magenta (`5`).
- Active range, search focus, links, and homepage roles use cyan (`6`). The active range is also bold, underlined, and bracketed.
- Formula, cask, and font labels use blue (`4`), magenta (`5`), and yellow (`3`) respectively.
- Success uses green (`2`); warnings, stale state, and install prompts use yellow (`3`); errors use red (`1`); secondary/help text uses bright black (`8`).
- The selected row uses cyan foreground plus reverse video and a visible `›` marker, producing a palette-driven selection without painting a background directly.
- `NO_COLOR` removes every foreground/background color while preserving bold, underline, reverse video, brackets, labels, and markers, so no state depends on color alone.

## 6. Architecture

### 6.1 Technology

- Go module: `github.com/milo/brewnicle`
- TUI runtime: Bubble Tea
- TUI components: Bubbles
- Styling and layout: Lip Gloss
- Local index: SQLite through a pure-Go driver; no CGO requirement
- Git history: the installed `git` executable, invoked directly
- HTTP: Go standard library

### 6.2 Components

#### Domain model

Defines package kind, package metadata, time range, and filtering/sorting behavior. It has no dependency on HTTP, git, SQLite, or Bubble Tea.

#### Catalog client

Fetches and validates:

- `https://formulae.brew.sh/api/formula.json`;
- `https://formulae.brew.sh/api/cask.json`.

It converts each eligible current API entry to a normalized catalog record containing identifier, kind, description, homepage, install target, known former identifiers, and the upstream `ruby_source_path` used for compatibility validation. Formula `name` and cask `token` are canonical identifiers. Entries whose API boolean field `disabled` is `true` are excluded before persistence; `disabled: false` or an omitted field remains eligible. Identifiers must match `[A-Za-z0-9][A-Za-z0-9+_.@-]*`. Missing descriptions become an empty string and render as `No description available`. A missing homepage or a value that does not parse as an absolute `http` or `https` URL becomes an empty string and disables `o`; homepage invalidity alone never drops an otherwise valid package. Malformed identifiers or entries without a canonical name/token are rejected. HTTP responses must be successful JSON, are limited to 64 MiB per response, and must be cancellable through context.

#### History cache and scanner

Owns partial, bare git repositories under Brewnicle's OS-specific cache directory. It never reads, writes, fetches, resets, or reconfigures repositories under Homebrew's installation or tap directories.

Repositories:

- `https://github.com/Homebrew/homebrew-core.git`;
- `https://github.com/Homebrew/homebrew-cask.git`.

The cache discovers and follows each remote's default branch. Initial clone and subsequent fetches request commit/tree history without package source blobs where supported. A repository update failure leaves its last valid cache intact.

The scanner performs bulk history passes rather than one git process per package. It reads add events and their commit timestamps, applies repository-specific path matching, derives identifiers from canonical Ruby filenames, and keeps the minimum timestamp per identifier. Eligible paths are anchored at the repository root:

- core legacy layout: `Formula/<name>.rb`;
- core bucketed layout: `Formula/<bucket>/<name>.rb`, where `<bucket>` is one lowercase ASCII letter or digit;
- core library layout: `Formula/lib/<name>.rb`;
- cask legacy layout: `Casks/<token>.rb`;
- cask bucketed layout: `Casks/<bucket>/<token>.rb`, where `<bucket>` is one lowercase ASCII letter or digit;
- font cask layout: `Casks/font/font-<bucket>/<token>.rb`, where `<bucket>` is one lowercase ASCII letter or digit and `<token>` begins with `font-`.

Only paths matching those exact families and the catalog identifier syntax participate. Other multi-character buckets, deeper paths, other roots, and arbitrary Ruby files are ignored. An opt-in live test verifies that every enabled current catalog `ruby_source_path` maps back to its canonical identifier and kind. Directory changes between a supported legacy and bucketed path with the same filename converge on the same identifier. Catalog records resolve their date as the minimum event for their canonical identifier and known former identifiers. Font records use the same cask event map; when their earlier pre-migration event is not reachable from `Homebrew/homebrew-cask`, their date remains unknown.

Git process invocation uses explicit arguments and context cancellation. Parser code tolerates irrelevant paths but treats malformed git output or a failed git command as a refresh failure rather than publishing partial dates.

#### Store

Owns the SQLite schema, loading, and publication of a refreshed index. It exposes package rows and refresh metadata to the application; UI code does not issue SQL.

#### Platform actions

Validates and executes homepage and installation actions. It is the sole component allowed to start `brew`, `open`, or `xdg-open` processes.

#### TUI application

Owns Bubble Tea state, commands, keyboard routing, responsive layout, progress, modals, and status messages. It consumes domain records and component interfaces; it does not parse API responses, git output, or SQL rows.

### 6.3 Project layout

```text
cmd/brewnicle/          executable entry point and dependency wiring
internal/domain/        package model, kinds, ranges, filter and sort logic
internal/catalog/       formulae.brew.sh HTTP client and normalization
internal/history/       app-owned git cache and bulk first-add scanner
internal/store/         SQLite schema, load, metadata, atomic publication
internal/platform/      brew and OS opener process actions
internal/ui/            Bubble Tea model, messages, views, keys, styles
```

Tests are colocated with their packages. Cross-component test fixtures live under the owning package's `testdata` directory.

## 7. Data Model

The SQLite database is an application cache, not a source of truth. It can always be rebuilt from the official API catalogs and git histories.

```sql
CREATE TABLE packages (
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('formula', 'cask', 'font')),
    description     TEXT NOT NULL,
    homepage        TEXT NOT NULL,
    added_at        INTEGER,
    install_target  TEXT NOT NULL,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (kind, name)
);

CREATE INDEX packages_added_at_idx ON packages (added_at);
CREATE INDEX packages_kind_idx ON packages (kind);

CREATE TABLE metadata (
    key   TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
);
```

`added_at` and `updated_at` are Unix seconds in UTC. A null `added_at` means unresolved history. `metadata` includes the schema version, the timestamp of the last successful complete refresh, and a separate history-layout algorithm version. Missing, invalid, or older algorithm versions remain readable but require a background refresh; current and future numeric versions do not refresh solely for this reason.

The application loads all package rows into memory after opening the database. The current catalog is small enough for in-memory time filtering, substring search, sorting, and selection. SQLite FTS is intentionally not used.

## 8. Data Flow

### 8.1 Startup with a valid index

1. Resolve the OS-specific Brewnicle cache directory.
2. Open and validate the active database.
3. Load package records, last-successful-refresh metadata, and the history-layout algorithm version. Legacy indexes without the algorithm key remain valid and readable.
4. Set the active time range to `30d` and render the cached records immediately.
5. If the successful refresh is less than 24 hours old and the algorithm version is current or newer, do no network work.
6. If it is at least 24 hours old, or its algorithm version is missing, invalid, or older, start one non-blocking background refresh while the cached records remain browsable.

### 8.2 First-run bootstrap

1. Show a foreground bootstrap screen while fetching the current API catalogs.
2. After both catalogs validate, enter provisional browsing with the current catalog in memory, force the `all` range, and mark exact dates and bounded ranges as indexing. Search, type filters, homepage opening, and installation remain usable.
3. Create or update the app-owned bare partial git caches in the background. Validate each completed full clone and atomically rename it to a durable `.bootstrap` path before scanning so a later run can reuse the download.
4. Scan exact history event maps from the durable pending repositories and promote each repository to its canonical cache path only after its scan and candidate state validate.
5. Exclude disabled entries, then join every remaining valid current catalog record to its earliest known date.
6. Build and validate a temporary SQLite database in the same directory as the active database.
7. Close it and atomically rename it into the active location, then replace the provisional rows while preserving search, type, and selection where possible.

Provisional rows are never written to SQLite and a pending clone never implies complete history or published refs. If catalog fetch fails before provisional browsing is possible, show the cause with retry and quit actions. If later history work fails, retain catalog browsing, mark dates unavailable, and offer `r` retry.

### 8.3 Refresh

Automatic and forced refreshes use the same pipeline as bootstrap. A forced refresh starts when `r` is pressed unless a refresh is already active. The current index remains visible throughout.

Publication is transactional and atomic: all normalized records and metadata are committed in the temporary database, validation confirms the expected schema and non-empty non-disabled current catalog, and an OS rename on the same filesystem replaces the active database. The prior active database is retained until the replacement is ready. On any earlier failure, the temporary database is removed and the active database is unchanged.

After successful publication, the application replaces its in-memory slice in one model update, reapplies the active range and search, and retains selection by the `(kind, name)` package key where possible.

### 8.4 Staleness

The 24-hour threshold is measured from the last successfully published complete refresh, not from an attempted refresh or a git file modification time. A failed refresh does not advance it. Automatic staleness checking occurs only once during startup. Brewnicle does not schedule a timer when a long-running process crosses the threshold; the user can press `r` to refresh that session. While stale cached data is displayed, the header states that it is stale and whether a retry is active.

## 9. Cache Locations and Trade-offs

Brewnicle uses the platform user-cache location, with an application subdirectory named `brewnicle`. It stores:

- the active SQLite index;
- temporary database files during refresh;
- bare partial git caches for core and cask history;
- durable `.bootstrap` clones that completed download but have not yet completed and published an exact scan.

The first run is materially slower than subsequent starts and requires network access, `git`, and enough disk for the repositories' commit/tree histories. Partial clone avoids package file blobs where the remote and local git version support filtering, but Homebrew's long histories can still consume significant time, bandwidth, and disk. Brewnicle must communicate this before bootstrap and show progress; it must not claim that initialization is instant or assign an exact size/time that varies by repository and network.

This cost is the deliberate trade-off for accurate, locally queryable all-time first-add dates without operating a hosted Brewnicle index. Later starts render SQLite data immediately, and later refreshes normally transfer only new git objects and current API catalogs.

## 10. UI and Operational States

The Bubble Tea model has explicit states:

- `bootstrapping`: no valid index and the current catalogs are not yet available;
- `browsing`: main split or narrow view, including provisional catalog-first browsing while dates index;
- `searching`: search input owns text keys;
- `help`: help overlay over browsing;
- `confirmingInstall`: modal owns confirmation/cancel keys;
- `runningInstall`: TUI temporarily yielded to the Homebrew process;
- `showingDetails`: narrow-layout selected-package detail;
- `fatalError`: no valid index and bootstrap cannot continue without retry/quit.

Refresh is orthogonal background state layered on browsing rather than a separate blocking screen when a valid index exists. Only one refresh may run at a time.

The browsing state starts with `30d` when exact dates are available and with forced `all` during provisional date indexing. Empty results show the active constraints and hints to clear search or choose `all`. A missing or invalid homepage has already been normalized to empty and disables `o` for that record. Errors are concise in the main view, while bootstrap and refresh summaries retain enough detail to identify whether HTTP, git, database, opener, or Homebrew failed.

## 11. Failure Handling

| Condition | Required behavior |
|---|---|
| Network unavailable with no index | Stay on first-run error screen; offer retry and quit |
| Network unavailable with valid index | Keep browsing; show stale/refresh warning |
| API response invalid or oversized | Abort refresh; do not publish partial catalog |
| `git` unavailable after first-run catalogs load | Keep provisional catalog browsing; mark exact dates unavailable and offer retry |
| `git` unavailable with valid index | Preserve index; report refresh failure |
| One catalog entry malformed | Skip it, count it, and show the skipped count in refresh summary |
| Git command/output malformed | Abort refresh because dates could be silently incorrect |
| A package has no history match | Store a null date; include it only in `all` |
| Temporary database build fails | Remove temporary artifacts; keep active index |
| Active database cannot be opened or validated | Preserve it under a diagnostic suffix and rebuild; if rebuild fails, report both facts |
| Homepage action fails | Return to browsing with error; no state loss |
| Homebrew install exits non-zero | Resume TUI and show its exit result; index unchanged |
| Terminal resized too small | Show minimum-size message; preserve selection and filters |

## 12. Security and Trust Boundaries

- API JSON and git output are untrusted input and are validated before persistence.
- Each HTTP body is limited to 64 MiB; non-2xx responses are errors.
- Canonical package identifiers must match `[A-Za-z0-9][A-Za-z0-9+_.@-]*` and must originate in the normalized current catalog.
- Install and opener commands use direct executable argument arrays. No feature invokes a shell.
- Non-empty homepage URLs must parse successfully and use only `http` or `https`; missing or invalid values normalize to empty and cannot reach the opener.
- Database files and git repositories are created only below the resolved Brewnicle cache directory.
- Refresh paths are cleaned and joined defensively so catalog or repository content cannot choose filesystem destinations.
- The app does not request elevated privileges or hide Homebrew prompts.
- Process execution is cancellable where safe; forced application exit must not publish an incomplete index.

## 13. Testing Strategy

### 13.1 Domain tests

Table-driven tests cover:

- exact 7-, 30-, 90-, and 365-day boundaries;
- UTC normalization;
- unknown dates in bounded and `all` ranges;
- newest-first ordering and name tie-breaking;
- case-insensitive name/description search;
- combined range and search behavior;
- formula, cask, and `font-` classification.

### 13.2 Catalog tests

Use `httptest.Server` and representative formula/cask fixtures to cover:

- normalization of name/token, description, homepage, and former identifiers;
- exclusion of formula and cask entries with `disabled: true`, while false or omitted values remain eligible;
- font classification without cask duplication;
- missing descriptions and missing/invalid homepages, including retention with `o` disabled;
- malformed identifiers and entries;
- non-2xx status, invalid JSON, cancellation, and response-size enforcement.

Tests do not require live network access.

### 13.3 History tests

Build tiny temporary git repositories in tests and cover:

- initial adds in `Formula/<name>.rb` and `Casks/<token>.rb` legacy layouts;
- initial adds in one-character bucketed formula and cask layouts, `Formula/lib`, and nested `Casks/font/font-<bucket>` layouts;
- same-basename moves between supported legacy and bucketed paths;
- rename resolved through a former identifier;
- delete and re-add retaining the first date;
- deeper paths, unknown roots, and unrelated Ruby files ignored;
- font date resolved from reachable cask history or left unknown when no event is reachable;
- malformed output and failed git command aborting the scan.

The bulk scanner test asserts that correctness does not depend on launching one git process per package.

### 13.4 Store tests

Cover schema creation, schema-version validation, null dates, round-trip loading, successful temporary-database promotion, rollback/cleanup on failure, preservation of the active index, last-successful-refresh semantics, and missing/old/current/future/invalid history-layout version behavior.

### 13.5 Platform action tests

Inject process runners and assert exact executable/argument arrays:

- `brew install name` for formulae;
- `brew install --cask token` for casks and fonts;
- `open URL` on macOS;
- `xdg-open URL` on Linux.

Reject hostile/non-catalog identifiers, unsupported schemes, missing commands, cancellation, and non-zero exits. No test invokes a real install.

### 13.6 TUI tests

Test Bubble Tea model transitions for every documented key, the default `30d` range, search ownership, confirmation/cancel behavior, refresh deduplication, startup-only staleness behavior, cached-data visibility during refresh, selection retention, action-result messages, help, empty results, fatal first-run errors, and resize transitions.

Render stable golden views with color disabled for wide split-pane, narrow list/detail, terminal-too-small, bootstrap, modal, stale-cache, and empty-result states.

### 13.7 Validation commands

The implementation is complete only when these pass:

```sh
go test ./...
go vet ./...
```

A separately named opt-in integration test may validate catalog compatibility and git-history scanning against live official endpoints. It is not part of the default test command and must not install packages.

## 14. Acceptance Criteria

1. On first run with network and git available, Brewnicle builds an index from the current official formula/cask APIs and the official core/cask histories, then displays current non-disabled packages.
2. Formulae, casks, and fonts appear exactly once with the correct kind; removed and API-disabled packages do not appear. Fonts are tagged casks, never duplicates.
3. Each resolved package uses its earliest known upstream add event across its current/former names and applicable repository; unresolved dates, including unreachable pre-migration font dates, are visibly unknown.
4. The application opens with `30d` and package type `all` active. Keys `1` through `5` and `tab`/`shift+tab` switch time ranges; `f`/`F` cycle package types forward/backward.
5. Unknown-date packages appear in time range `all` and in no bounded range.
6. Package type, time range, and `/` name/description search combine with logical AND; active filters survive refresh and responsive state changes.
7. Wide terminals show the approved split pane; narrow terminals provide list/detail toggling; undersized terminals do not corrupt rendering. The selected list row is centered when possible using the lower middle for even capacities and full-page clamping at boundaries.
8. The UI uses only the approved terminal ANSI `1`–`8` role mapping, sets no explicit background, RGB, ANSI-256, or adaptive colors, uses redundant attribute/text cues including reverse video for selection, and removes every color while remaining understandable under `NO_COLOR`.
9. `o` opens only validated HTTP(S) homepages through the correct OS command without a shell. Missing or invalid homepages retain the package with an empty value and `o` disabled.
10. `i` shows the exact install command and runs nothing unless confirmed. Confirmed formula installs use `brew install <name>`; cask/font installs use `brew install --cask <token>` with attached stdio, and the TUI resumes afterward.
11. A valid cached index renders before refresh. If it is at least 24 hours stale or has a missing, invalid, or older history-layout version when the process starts, refresh begins once in the background; no later timer is scheduled in a long-running process. `r` forces refresh, and only one refresh runs at once.
12. Any failed refresh leaves the prior active database and visible records intact and reports the failure.
13. Refresh publication is transactional and atomic; no partial package set becomes active.
14. Homebrew tap repositories are never modified. All git and database cache data remains under Brewnicle's user-cache directory.
15. `go test ./...` and `go vet ./...` pass.

## 15. Implementation Sequence

1. Establish the Go module and domain types with filter/search tests.
2. Implement and test catalog normalization.
3. Implement app-owned git caching and bulk history scanning using fixture repositories.
4. Implement the pure-Go SQLite store and atomic publication.
5. Implement platform actions behind injected process interfaces.
6. Build the Bubble Tea model and states, then the approved split/narrow views and terminal-native styles.
7. Wire bootstrap, cached startup, refresh, and action flows in the executable.
8. Run full tests and vet, then perform a manual read-only browse/refresh smoke test before separately confirming any real install action.

## Incremental History Refresh Amendment (2026-08-09)

The active SQLite generation is the authoritative refresh checkpoint. Schema v2 retains current enabled package rows plus a complete per-repository `history_events` earliest-add aggregate and exact `history_repos` core/cask commit cursors. Removed identifiers remain in the aggregate for future re-add and former-name resolution, but never appear in the current package list.

A valid schema-v1 index remains readable and visible while one background migration performs the final CPU-intensive complete scan using the existing app-owned Git caches. Migration failure leaves schema v1 active. For a complete current schema-v2 state, an unchanged official tip performs no history log; a normal fast-forward scans exactly `old_oid..new_oid` and merges timestamps with `MIN`; a non-ancestor, missing object, shallow/corrupt repository, or algorithm mismatch fully rebuilds only the affected repository from the current official reachable history. Full and incremental scans use immutable OIDs, strict NUL framing, `--full-history`, and combined merge diff semantics so reachable side-branch and merge-resolution additions are retained without assigning artificial merge timestamps.

Fetched and published Git refs are object pins and recovery aids, not database cursors. Packages, both complete aggregates, both exact cursors, versions, and the publication timestamp are written to a validated sibling database and become active at atomic rename. Pre-rename failure preserves the previous generation. Directory-sync, post-publication ref reconciliation, or lock-release failure is reported as a warning and does not misreport the visible new generation as rolled back. Compatibility `candidate` and `last-good` refs can retain orphaned objects after rewritten history until a later cleanup change.

A macOS/Linux OS file lock covers active-index reload, catalog fetch, both repository preparations, publication, and ref reconciliation. Concurrent processes report that they are waiting, then reload the winning generation after acquiring the lock rather than publishing competing work from a stale base. The publication timestamp is captured at SQLite whole-second precision after history preparation. UI time/type/search state never enters the refresh service or alters history scope.
