# Brewnicle Design Specification

**Date:** 2026-07-26  
**Status:** Approved for implementation  
**Go module:** `github.com/milo/brewnicle`

## 1. Summary

Brewnicle is a Go terminal application for discovering packages recently added to the official Homebrew catalog. It presents current formulae, casks, and font casks in a Charm split-pane TUI with package descriptions, upstream addition dates, time-range filters, search, homepage opening, and confirmed installation.

Homebrew's own `brew update` report cannot supply the required history. In install-from-API mode it compares newline-delimited snapshots such as `$(brew --cache)/api/formula_names.before.txt` with `formula_names.txt`, and the equivalent cask files, to produce the `New Formulae` and `New Casks` sections. Those files retain only the delta from the previous local update. The formula and cask API catalogs provide names, descriptions, and homepages, but no package-added timestamp. Homebrew has no durable local database of everything it has ever advertised as new.

Brewnicle therefore joins the current official API catalog with first-add events derived from the upstream git histories. It displays only packages still available in the current catalog; it does not retain removed packages.

## 2. Goals

1. Show every currently available official Homebrew formula and cask, including fonts.
2. Date each package by its earliest known addition to the relevant upstream Homebrew git history.
3. Filter packages added within the last 7, 30, 90, or 365 days, or show all current packages.
4. Search package names and descriptions interactively.
5. Show a selected package's description, exact addition date, homepage, and install command.
6. Open a selected package's homepage.
7. Install a selected package only after explicit confirmation.
8. Remain immediately useful from a valid local index when offline or when refresh fails.
9. Preserve the user's terminal palette rather than imposing an application color theme.

## 3. Non-goals

The first release does not include:

- deleted or otherwise unavailable historical packages;
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

### 4.1 Current catalog only

The current formula and cask API responses are authoritative for membership. A record absent from the current catalog is absent from Brewnicle even if it exists in git history. This keeps every listed package actionable and prevents stale history from appearing as installable software.

### 4.2 Package kinds

Brewnicle exposes three kinds:

- `formula`: entries from the formula API;
- `cask`: non-font entries from the cask API;
- `font`: cask entries whose current token begins with `font-`.

A font is stored once with kind `font`; it is not duplicated as a cask. Fonts use cask installation semantics. The historical `homebrew-cask-fonts` repository participates only in date resolution for fonts that existed before their history moved into the main cask repository.

### 4.3 Addition date

`added_at` is the earliest known upstream commit's committer timestamp at which the package, or one of its known former names, was added in an applicable official repository. Timestamps are normalized to UTC.

Rules:

- Formulae are resolved against `Homebrew/homebrew-core`.
- Casks are resolved against `Homebrew/homebrew-cask`.
- Fonts are resolved against both `Homebrew/homebrew-cask` and historical `Homebrew/homebrew-cask-fonts`; the earliest match wins.
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
| `all` | every current package, including unknown dates |

Unknown dates are excluded from every bounded filter. Results are sorted by `added_at` descending, then package name ascending. Unknown dates sort after all dated records in the `all` view.

Search is a case-insensitive substring match over package name and description. Search and the active time filter are combined with logical AND.

## 5. User Experience

### 5.1 Main layout

The selected wide-terminal design is a split pane.

**Header**

- application name;
- time tabs: `7d`, `30d`, `90d`, `1y`, `all`;
- active search query, when present;
- visible and total package counts;
- stale or refreshing indicator when applicable.

**Left pane**

- scrollable current package list;
- package name and compact `formula`, `cask`, or `font` badge;
- newest-first ordering defined in section 4.4;
- selection retained across filter changes when the selected record remains visible, otherwise moved to the first visible record.

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

The command is executed directly with an argument vector, not through a shell. A missing homepage, invalid URL scheme, unsupported operating system, missing opener, or opener failure is reported without leaving the TUI in a broken state.

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

These breakpoint values belong to the UI package as named constants and are fixed by rendering tests rather than user configuration.

### 5.7 Terminal-native styling

Brewnicle preserves the terminal's default foreground and background. It does not paint a full-screen background and does not use RGB or 256-color literals.

- Optional accents use standard ANSI palette entries so the terminal theme controls their appearance.
- The selected row uses reverse video.
- Borders, spacing, and text weight carry meaning without color.
- When `NO_COLOR` is present, optional color accents are disabled; selection, focus, warnings, and errors remain distinguishable through reverse video, labels, borders, and text weight.

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

It converts each current API entry to a normalized catalog record containing identifier, kind, description, homepage, install target, and known former identifiers. Formula `name` and cask `token` are canonical identifiers. Identifiers must match `[A-Za-z0-9][A-Za-z0-9+_.@-]*`. Missing descriptions become an empty string and render as `No description available`; malformed identifiers or entries without a canonical name/token are rejected. HTTP responses must be successful JSON, are limited to 64 MiB per response, and must be cancellable through context.

#### History cache and scanner

Owns partial, bare git repositories under Brewnicle's OS-specific cache directory. It never reads, writes, fetches, resets, or reconfigures repositories under Homebrew's installation or tap directories.

Repositories:

- `https://github.com/Homebrew/homebrew-core.git`;
- `https://github.com/Homebrew/homebrew-cask.git`;
- `https://github.com/Homebrew/homebrew-cask-fonts.git` for historical font resolution.

The cache discovers and follows each remote's default branch. Initial clone and subsequent fetches request commit/tree history without package source blobs where supported. A repository update failure leaves its last valid cache intact.

The scanner performs bulk history passes rather than one git process per package. It reads add events and their commit timestamps, derives identifiers from formula/cask Ruby filenames, and keeps the minimum timestamp per identifier. Directory changes with the same filename converge on the same identifier. Catalog records then resolve their date as the minimum event for their canonical identifier and known former identifiers. Font records additionally consult the historical fonts event map.

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

`added_at` and `updated_at` are Unix seconds in UTC. A null `added_at` means unresolved history. `metadata` includes the schema version and the timestamp of the last successful complete refresh.

The application loads all package rows into memory after opening the database. The current catalog is small enough for in-memory time filtering, substring search, sorting, and selection. SQLite FTS is intentionally not used.

## 8. Data Flow

### 8.1 Startup with a valid index

1. Resolve the OS-specific Brewnicle cache directory.
2. Open and validate the active database.
3. Load package records and last-successful-refresh metadata.
4. Render the cached records immediately.
5. If the successful refresh is less than 24 hours old, do no network work.
6. If it is at least 24 hours old, start a non-blocking background refresh while the cached records remain browsable.

### 8.2 First-run bootstrap

1. Show a foreground bootstrap screen with phase, current repository, and progress where measurable.
2. Fetch both current API catalogs.
3. Create or update the app-owned bare partial git caches.
4. Scan history event maps.
5. Join every valid current catalog record to its earliest known date.
6. Build and validate a temporary SQLite database in the same directory as the active database.
7. Close it and atomically rename it into the active location.
8. Load the new rows and enter the main view.

No partially built index is made visible. If first-run bootstrap fails, show the cause with retry and quit actions.

### 8.3 Refresh

Automatic and forced refreshes use the same pipeline as bootstrap. A forced refresh starts when `r` is pressed unless a refresh is already active. The current index remains visible throughout.

Publication is transactional and atomic: all normalized records and metadata are committed in the temporary database, validation confirms the expected schema and non-empty current catalog, and an OS rename on the same filesystem replaces the active database. The prior active database is retained until the replacement is ready. On any earlier failure, the temporary database is removed and the active database is unchanged.

After successful publication, the application replaces its in-memory slice in one model update, reapplies the active range and search, and retains selection by the `(kind, name)` package key where possible.

### 8.4 Staleness

The 24-hour threshold is measured from the last successfully published complete refresh, not from an attempted refresh or a git file modification time. A failed refresh does not advance it. While stale cached data is displayed, the header states that it is stale and whether a retry is active.

## 9. Cache Locations and Trade-offs

Brewnicle uses the platform user-cache location, with an application subdirectory named `brewnicle`. It stores:

- the active SQLite index;
- temporary database files during refresh;
- bare partial git caches for core, cask, and historical fonts history.

The first run is materially slower than subsequent starts and requires network access, `git`, and enough disk for the repositories' commit/tree histories. Partial clone avoids package file blobs where the remote and local git version support filtering, but Homebrew's long histories can still consume significant time, bandwidth, and disk. Brewnicle must communicate this before bootstrap and show progress; it must not claim that initialization is instant or assign an exact size/time that varies by repository and network.

This cost is the deliberate trade-off for accurate, locally queryable all-time first-add dates without operating a hosted Brewnicle index. Later starts render SQLite data immediately, and later refreshes normally transfer only new git objects and current API catalogs.

## 10. UI and Operational States

The Bubble Tea model has explicit states:

- `bootstrapping`: no valid index; foreground catalog/history/index progress;
- `browsing`: main split or narrow view;
- `searching`: search input owns text keys;
- `help`: help overlay over browsing;
- `confirmingInstall`: modal owns confirmation/cancel keys;
- `runningInstall`: TUI temporarily yielded to the Homebrew process;
- `showingDetails`: narrow-layout selected-package detail;
- `fatalError`: no valid index and bootstrap cannot continue without retry/quit.

Refresh is orthogonal background state layered on browsing rather than a separate blocking screen when a valid index exists. Only one refresh may run at a time.

Empty results show the active constraints and hints to clear search or choose `all`. A missing selected homepage disables `o` for that record. Errors are concise in the main view, while bootstrap and refresh summaries retain enough detail to identify whether HTTP, git, database, opener, or Homebrew failed.

## 11. Failure Handling

| Condition | Required behavior |
|---|---|
| Network unavailable with no index | Stay on first-run error screen; offer retry and quit |
| Network unavailable with valid index | Keep browsing; show stale/refresh warning |
| API response invalid or oversized | Abort refresh; do not publish partial catalog |
| `git` unavailable during first run | Explain that git is required for history; offer retry and quit |
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
- Homepage URLs must parse successfully and use only `http` or `https`.
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
- font classification without cask duplication;
- missing optional fields;
- malformed identifiers and entries;
- non-2xx status, invalid JSON, cancellation, and response-size enforcement.

Tests do not require live network access.

### 13.3 History tests

Build tiny temporary git repositories in tests and cover:

- initial add;
- same-basename path move;
- rename resolved through a former identifier;
- delete and re-add retaining the first date;
- unrelated Ruby files ignored;
- font date chosen from the earlier of cask and historical fonts history;
- malformed output and failed git command aborting the scan.

The bulk scanner test asserts that correctness does not depend on launching one git process per package.

### 13.4 Store tests

Cover schema creation, schema-version validation, null dates, round-trip loading, successful temporary-database promotion, rollback/cleanup on failure, preservation of the active index, and last-successful-refresh semantics.

### 13.5 Platform action tests

Inject process runners and assert exact executable/argument arrays:

- `brew install name` for formulae;
- `brew install --cask token` for casks and fonts;
- `open URL` on macOS;
- `xdg-open URL` on Linux.

Reject hostile/non-catalog identifiers, unsupported schemes, missing commands, cancellation, and non-zero exits. No test invokes a real install.

### 13.6 TUI tests

Test Bubble Tea model transitions for every documented key, search ownership, confirmation/cancel behavior, refresh deduplication, cached-data visibility during refresh, selection retention, action-result messages, help, empty results, fatal first-run errors, and resize transitions.

Render stable golden views with color disabled for wide split-pane, narrow list/detail, terminal-too-small, bootstrap, modal, stale-cache, and empty-result states.

### 13.7 Validation commands

The implementation is complete only when these pass:

```sh
go test ./...
go vet ./...
```

A separately named opt-in integration test may validate catalog compatibility and git-history scanning against live official endpoints. It is not part of the default test command and must not install packages.

## 14. Acceptance Criteria

1. On first run with network and git available, Brewnicle builds an index from the current official formula/cask APIs and the three specified official histories, then displays current packages.
2. Formulae, casks, and fonts appear exactly once with the correct kind; removed packages do not appear.
3. Each resolved package uses its earliest known upstream add event across its current/former names and applicable repositories; unresolved dates are visibly unknown.
4. Keys `1` through `5` and `tab`/`shift+tab` switch among `7d`, `30d`, `90d`, `1y`, and `all` using the boundary rules in this specification.
5. Unknown-date packages appear in `all` and in no bounded range.
6. `/` search filters name and description case-insensitively and combines with the active range.
7. Wide terminals show the approved split pane; narrow terminals provide list/detail toggling; undersized terminals do not corrupt rendering.
8. The UI preserves default terminal foreground/background, uses only theme-controlled ANSI accents, uses reverse video for selection, and remains understandable with `NO_COLOR`.
9. `o` opens only validated HTTP(S) homepages through the correct OS command without a shell.
10. `i` shows the exact install command and runs nothing unless confirmed. Confirmed formula installs use `brew install <name>`; cask/font installs use `brew install --cask <token>` with attached stdio, and the TUI resumes afterward.
11. A valid cached index renders before refresh. At 24 hours stale, refresh begins in the background; `r` forces it; only one refresh runs at once.
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
