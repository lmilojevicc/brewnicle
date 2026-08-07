# Implementation Plan

## Goal

Build Brewnicle as a tested Go/Charm TUI that derives upstream Homebrew package addition dates, caches a current installable catalog locally, and supports responsive browsing, search, homepage opening, and confirmed installation.

## Approved review amendments

These amendments supersede conflicting wording later in the generated plan:

1. Historical bucket directories match exactly `[a-z0-9]`; uppercase, non-ASCII, empty, multi-character, and deeper buckets are rejected.
2. Git uses the exact official HTTPS remotes and requests `--filter=blob:none` for clone and fetch. A filter-related clone retry uses a fresh temporary directory, and diagnostics say only that filtering was requested unless effectiveness was verified.
3. Git history is streamed with NUL-delimited paths and strict commit framing; full history output is never buffered.
4. The Bubble Tea model solely owns refresh deduplication. Startup queues at most one refresh, bootstrap renders before blocking work, and progress crosses an immutable message channel rather than mutating model state from a goroutine.
5. Install commands retain nil stdio; Bubble Tea `ExecProcess` owns terminal release, stream attachment, restoration, and completion messaging.
6. SQLite rename is the publication commit point. A later directory-sync error is a durability warning and the newly published rows remain active.
7. Direct versions are pinned to Go 1.25.0, Bubble Tea 1.3.10, Bubbles 1.0.0, Lip Gloss 1.1.0, and modernc SQLite 1.56.0.
8. Package ordering uses kind ascending as a deterministic tertiary key after addition date descending and name ascending.
9. Fixture smoke testing uses isolated `HOME`/`XDG_CACHE_HOME` and test-only store wiring; no public cache override flag is added.

## Tasks

1. **Bootstrap the Go module and implement the domain layer**
   - Files:
     - `go.mod`
     - `go.sum`
     - `cmd/brewnicle/main.go`
     - `internal/domain/package.go`
     - `internal/domain/filter.go`
     - `internal/domain/filter_test.go`
   - Changes:
     - Initialize module `github.com/milo/brewnicle`.
     - Add only these direct dependencies:
       - `github.com/charmbracelet/bubbletea` — Elm-style TUI runtime and subprocess handoff.
       - `github.com/charmbracelet/bubbles` — search input and help/key components.
       - `github.com/charmbracelet/lipgloss` — terminal layout and ANSI styling.
       - `modernc.org/sqlite` — `database/sql` SQLite driver without CGO.
     - Pin the versions resolved into `go.mod`/`go.sum`; use the standard library for HTTP, JSON, URL handling, process execution, paths, and testing. Do not add Cobra, Testify, an ORM, or an FTS dependency.
     - Define:
       - `KindFormula`, `KindCask`, `KindFont`.
       - `Package` with name, kind, description, homepage, nullable addition time, install target, former identifiers, and update time.
       - `Range7D`, `Range30D`, `Range90D`, `Range1Y`, `RangeAll`, with `30d` as the default.
       - A distinct `KindFilter` with `all`, `formula`, `cask`, and `font`, defaulting to `all`; do not overload package `Kind` with the filter-only `all` state.
       - Stable package identity as `(kind, name)`.
     - Implement pure type/range filtering, case-insensitive search, UTC boundary handling, and sorting. Type, range, and search compose with logical AND. Unknown dates appear only in time range `all` and sort after dated records.
     - Keep `cmd/brewnicle/main.go` minimal but compilable; dependency wiring comes later.
   - Tests:
     - Exact 7/30/90/365-day boundaries using an injected `now`.
     - UTC normalization.
     - Unknown dates in bounded and all-time views.
     - Combined package-type, name/description search, and range filtering, including every type, invalid/default handling, fonts, and empty results.
     - Newest-first ordering and name tie-breaking.
   - Acceptance:
     - `go test ./internal/domain`
     - `go test ./...`
     - `go build ./cmd/brewnicle`
   - Checkpoint:
     - The module compiles and the first useful vertical slice—package semantics and filtering—is complete without external I/O.

2. **Implement the official catalog HTTP client and normalization**
   - Files:
     - `internal/catalog/client.go`
     - `internal/catalog/types.go`
     - `internal/catalog/normalize.go`
     - `internal/catalog/client_test.go`
     - `internal/catalog/testdata/formula.json`
     - `internal/catalog/testdata/cask.json`
   - Changes:
     - Fetch configurable formula and cask URLs, defaulting to:
       - `https://formulae.brew.sh/api/formula.json`
       - `https://formulae.brew.sh/api/cask.json`
     - Use an injected `http.Client` and context.
     - Enforce a 64 MiB limit per response with `io.LimitReader(max+1)` and reject oversized, non-2xx, invalid, or trailing malformed JSON.
     - Decode only required API fields:
       - Formula: `name`, `desc`, `homepage`, `oldnames`, `disabled`.
       - Cask: `token`, `desc`, `homepage`, `old_tokens`, `disabled`.
     - Exclude `disabled: true`; false or omitted remains eligible.
     - Validate canonical identifiers against `[A-Za-z0-9][A-Za-z0-9+_.@-]*`.
     - Skip entries lacking a valid canonical identifier and return a skipped-entry count rather than aborting the full catalog.
     - Filter malformed former identifiers individually so they cannot influence history lookup.
     - Normalize missing descriptions to empty.
     - Preserve packages with missing or invalid homepages, but normalize the homepage to empty unless it is an absolute HTTP(S) URL with a host.
     - Classify cask tokens beginning with `font-` as `font`; do not emit a duplicate cask record.
     - Keep formula and cask membership distinct even when names collide.
   - Tests:
     - Valid formula, cask, and font records.
     - Disabled true/false/omitted behavior.
     - Old-name normalization.
     - Invalid canonical and former identifiers.
     - Missing description and invalid homepage retention.
     - Non-2xx, cancellation, invalid JSON, and 64 MiB overflow.
     - Separate formula/cask skipped counts.
   - Acceptance:
     - `go test ./internal/catalog`
     - `go test ./...`
   - Dependencies:
     - Depends on Task 1 domain types.
   - Checkpoint:
     - Current non-disabled Homebrew catalog data can be normalized deterministically without git or SQLite.

3. **Implement history path matching and bulk first-add scanning**
   - Files:
     - `internal/history/path.go`
     - `internal/history/parser.go`
     - `internal/history/scanner.go`
     - `internal/history/path_test.go`
     - `internal/history/parser_test.go`
     - `internal/history/scanner_test.go`
   - Changes:
     - Define repository kinds for core and cask.
     - Accept only these root-anchored layouts:
       - `Formula/<name>.rb`
       - `Formula/<one-letter-or-digit>/<name>.rb`
       - `Formula/lib/<name>.rb`
       - `Casks/<token>.rb`
       - `Casks/<one-letter-or-digit>/<token>.rb`
       - `Casks/font/font-<one-letter-or-digit>/<font-token>.rb`
     - Require nested font tokens to begin with `font-`; reject other multi-character buckets, deeper paths, unrelated roots, arbitrary Ruby files, invalid identifiers, and mismatched core/cask layouts.
     - Decode catalog `ruby_source_path` and keep an opt-in live compatibility test that maps every enabled current path back to its canonical identifier and kind without cloning history.
     - Run one bulk `git log` process per repository/ref rather than one process per package.
     - Request added paths and committer timestamps with explicit separators, `--diff-filter=A`, and rename detection disabled. Parse records strictly; tolerate irrelevant paths but abort on structurally malformed output.
     - Use `exec.CommandContext` so cancellation terminates the git process.
     - Record the minimum UTC committer timestamp for every canonical filename. Path bucketing and delete/re-add events naturally converge on the earliest timestamp.
     - Resolve a package against its current identifier plus valid former identifiers, taking the earliest event. No match yields an unknown date.
     - Fonts use the cask event map only.
   - Tests:
     - Pure parser fixtures for valid records, irrelevant paths, bad timestamps, truncated records, and cancellation.
     - Temporary git repositories with fixed author/committer dates covering:
       - Legacy and bucketed additions.
       - Legacy-to-bucketed moves.
       - Former-name lookup.
       - Delete and re-add.
       - Unknown/deeper paths.
       - Unknown font history.
     - Assert the scanner starts one log process per repository, not per package.
     - Skip git-backed tests only when `git` is genuinely unavailable; parser tests must always run.
   - Acceptance:
     - `go test ./internal/history`
     - `go test ./...`
   - Dependencies:
     - Depends on Task 1 identifier and package semantics.
   - Technical mitigation:
     - Do not parse human-oriented git output. Use machine separators and fixed timestamp/path fields, validate every timestamp and path, and treat ambiguity as a refresh failure.

4. **Implement app-owned partial git repository caching**
   - Files:
     - `internal/history/cache.go`
     - `internal/history/git.go`
     - `internal/history/cache_test.go`
   - Changes:
     - Configure exactly two official remotes:
       - `Homebrew/homebrew-core`
       - `Homebrew/homebrew-cask`
     - Store bare repositories only below the Brewnicle cache directory; never inspect or mutate Homebrew tap directories.
     - Discover each remote’s default branch through a symbolic `HEAD` query and validate it as a branch ref.
     - For initial setup:
       - Clone into a sibling temporary directory.
       - Request `--bare --filter=blob:none --single-branch`.
       - Validate the candidate ref and scanner output.
       - Atomically rename the completed cache into place.
     - For refresh:
       - Fetch the default branch into a candidate ref.
       - Scan the candidate before advancing a dedicated last-good ref.
       - Advance last-good with `git update-ref` only after a successful fetch and scan.
       - A failed fetch or malformed scan may leave unreachable objects/candidate refs, but must not invalidate the last-good ref used by the current index.
     - Fall back cleanly when the remote/local Git version does not support partial-clone filtering; report the fallback rather than pretending initialization is small.
     - Expose phase and repository progress callbacks. Do not parse unstable Git percentage text; use honest phase-level progress.
     - Pass contexts through every command and return stderr excerpts with errors.
   - Tests:
     - Inject a command runner for branch discovery, clone/fetch argv, cancellation, and failure cases.
     - Use local temporary bare remotes to verify initial atomic creation, candidate scanning, last-good promotion, and preservation after failed refresh.
     - Assert cache paths cannot escape the configured root.
   - Acceptance:
     - `go test ./internal/history`
     - `go test ./...`
   - Dependencies:
     - Depends on Task 3 scanner.
   - Checkpoint:
     - Core/cask history can be updated and scanned without touching Homebrew and without losing the last valid history ref.

5. **Implement the SQLite index and crash-safe publication**
   - Files:
     - `internal/store/schema.go`
     - `internal/store/store.go`
     - `internal/store/publish.go`
     - `internal/store/store_test.go`
     - `internal/store/publish_test.go`
   - Changes:
     - Use `database/sql` with `modernc.org/sqlite`.
     - Create the approved `packages` and `metadata` schema with composite `(kind, name)` primary key and indexes on `added_at` and `kind`.
     - Persist UTC Unix seconds; use SQL NULL for unresolved `added_at`.
     - Store explicit schema version, last successful complete refresh, and a separate history-layout algorithm version. Legacy/malformed/older algorithm metadata remains readable but schedules one background refresh; current or future numeric versions do not refresh solely for this reason.
     - Keep database connections short-lived. Load all rows and metadata, then close the active connection before any later replacement.
     - Implement publication by:
       1. Creating a uniquely named sibling temporary DB with mode `0600`.
       2. Using `journal_mode=DELETE` and a transaction so no WAL/SHM sidecars are required for promotion.
       3. Inserting the full non-empty package set and metadata.
       4. Committing, closing, reopening read-only, and validating schema/count/metadata.
       5. Ensuring no SQLite handles remain open.
       6. Atomically renaming the sibling file over the active path.
       7. Syncing the parent directory where supported.
     - Remove temporary artifacts after any pre-publication failure; never delete or truncate the active index first.
     - Provide a helper to preserve an invalid active database under a timestamped diagnostic suffix before rebuild.
   - Tests:
     - Schema creation/version validation.
     - Formula/cask name collision round trip.
     - Null dates.
     - Stable sorting after load.
     - Successful replacement.
     - Insert/validation/rename failure preserving the original bytes.
     - No residual `.tmp`, `-wal`, or `-shm` files.
     - Invalid DB diagnostic preservation.
   - Acceptance:
     - `go test ./internal/store`
     - `go test -race ./internal/store`
     - `go test ./...`
   - Dependencies:
     - Depends on Task 1 domain types.
   - Technical mitigation:
     - Atomic replacement is supported only on the specified macOS/Linux target. Never rename a live WAL database or retain a long-lived connection to the old inode.

6. **Implement refresh orchestration and cache-path resolution**
   - Files:
     - `internal/refresh/paths.go`
     - `internal/refresh/service.go`
     - `internal/refresh/progress.go`
     - `internal/refresh/service_test.go`
     - `internal/refresh/paths_test.go`
   - Changes:
     - Resolve `os.UserCacheDir()/brewnicle`, create it with `0700`, and derive fixed child paths for the DB and git caches.
     - Validate/clean all derived paths; upstream names must never become filesystem paths.
     - Define narrow interfaces for catalog fetch, repository update/scan, and store publication so the pipeline is testable.
     - Implement the complete refresh:
       1. Fetch both catalogs.
       2. Update and scan core/cask candidates.
       3. Resolve earliest events across current/former names.
       4. Preserve unknown dates.
       5. Publish the complete joined index.
       6. Load and return the newly published rows and summary.
     - Emit phase/repository/skipped-record progress without speculative percentages.
     - Do not publish if either catalog, either required repository, scanning, joining, or DB validation fails.
     - Implement startup staleness as a pure decision: less than 24 hours is fresh; at least 24 hours is stale. It is evaluated once at process startup.
     - Keep refresh serialization in the consuming model, but make the service safe to cancel.
   - Tests:
     - Fully fake pipeline proving call order, joined dates, former names, unknown dates, and font behavior.
     - Any-stage failure leaves publisher uncalled.
     - Skipped catalog counts reach the summary.
     - Exact 24-hour staleness boundary.
     - Cache path behavior with injected cache roots.
     - Cancellation before publication.
   - Acceptance:
     - `go test ./internal/refresh`
     - `go test ./...`
   - Dependencies:
     - Depends on Tasks 2–5.
   - Checkpoint:
     - A headless end-to-end refresh can produce and reload a complete atomic index using fakes and temporary local repositories.

7. **Implement validated homepage and installation actions**
   - Files:
     - `internal/platform/runner.go`
     - `internal/platform/open.go`
     - `internal/platform/install.go`
     - `internal/platform/actions_test.go`
   - Changes:
     - Define injectable executable lookup and process-runner boundaries.
     - Build commands without a shell:
       - Formula: `brew install <name>`.
       - Cask/font: `brew install --cask <token>`.
       - macOS homepage: `open <url>`.
       - Linux homepage: `xdg-open <url>`.
     - Revalidate the selected loaded package’s canonical identifier and homepage before action construction.
     - Reject empty/non-HTTP(S) homepages and unsupported OS values.
     - Detect missing `brew` without preventing browsing.
     - Return typed action results suitable for status messages.
     - Keep install command construction separate from execution so UI tests never run Homebrew.
   - Tests:
     - Exact executable and argv for every package kind and supported OS.
     - Missing tools, unsupported OS, invalid URL/identifier, cancellation, and non-zero exit.
     - Assert no shell executable or concatenated command string is used.
   - Acceptance:
     - `go test ./internal/platform`
     - `go test ./...`
   - Dependencies:
     - Depends on Task 1 domain types.

8. **Implement the Bubble Tea model and interaction state machine**
   - Files:
     - `internal/ui/model.go`
     - `internal/ui/messages.go`
     - `internal/ui/keys.go`
     - `internal/ui/update.go`
     - `internal/ui/actions.go`
     - `internal/ui/model_test.go`
   - Changes:
     - Use Bubbles `textinput` for search and `key`/`help` for discoverable bindings; keep package-list selection logic local because the split/narrow behavior is application-specific.
     - Model explicit states:
       - Bootstrapping.
       - Browsing.
       - Searching.
       - Help.
       - Confirming install.
       - Running install.
       - Narrow details.
       - Fatal first-run error.
     - Represent refresh as orthogonal state over browsing; reject duplicate refresh requests.
     - Default every run to time range `30d` and package type `all`.
     - Route `1`–`5`, tab/shift-tab, movement, `f`/`F` package-type cycling, `/`, escape, `o`, `i`, `r`, `?`, enter, and quit according to the approved ownership rules.
     - Reapply shared domain filtering on query, range, or package-type changes.
     - Retain selection by `(kind, name)` when possible; otherwise select the first result.
     - Keep cached rows visible during refresh.
     - Replace rows in one message after successful publication.
     - Preserve all filters and selection across refresh replacement, resize, responsive state changes, and action completion.
     - Inject refresh/open/install commands as functions; the model must not perform HTTP, SQL, git, or raw process execution.
   - Tests:
     - Every documented key and state transition.
     - Search escape behavior.
     - Time/type filter cycling, defaults, logical-AND composition, modal key ownership, and selection retention/fallback.
     - Confirmation/cancellation runs nothing until confirmed.
     - Refresh deduplication and stale startup trigger.
     - Selection retention after refresh.
     - Empty results, action messages, fatal retry, and quit ownership.
   - Acceptance:
     - `go test ./internal/ui`
     - `go test ./...`
   - Dependencies:
     - Depends on Tasks 1, 6, and 7.
   - Checkpoint:
     - All user workflows function at the model level before rendering complexity is introduced.

9. **Implement responsive views and terminal-native styling**
   - Files:
     - `internal/ui/view.go`
     - `internal/ui/view_wide.go`
     - `internal/ui/view_narrow.go`
     - `internal/ui/styles.go`
     - `internal/ui/format.go`
     - `internal/ui/view_test.go`
     - `internal/ui/testdata/golden/wide.golden`
     - `internal/ui/testdata/golden/narrow-list.golden`
     - `internal/ui/testdata/golden/narrow-detail.golden`
     - `internal/ui/testdata/golden/too-small.golden`
     - `internal/ui/testdata/golden/bootstrap.golden`
     - `internal/ui/testdata/golden/confirm.golden`
     - `internal/ui/testdata/golden/stale.golden`
     - `internal/ui/testdata/golden/empty.golden`
   - Changes:
     - Define fixed breakpoint constants:
       - Wide: at least 90 columns and 16 rows.
       - Narrow: 50–89 columns and at least 12 rows.
       - Too small: below 50 columns or 12 rows.
     - Render wide split pane with list left and selected package detail right.
     - Render narrow list/detail toggle with enter.
     - Render only a minimum-size message and quit hint when too small.
     - Show header time tabs, active package type, query, counts, stale/refresh state; detail fields; exact install command; and context-aware footer/help hints for `f`/`F`.
     - Keep the selected list row vertically centered when possible using actual body capacity: exact middle for odd capacities, lower middle for even capacities, and full-page clamping without blank padding at the first/last boundaries. Derive this purely during rendering so movement, filters, refresh, and resize require no persistent scroll offset.
     - Wrap/truncate using measured Lip Gloss cell widths so no line or total height exceeds the last `WindowSizeMsg`.
     - Use the terminal ANSI palette for a vivid theme without fixed RGB:
       - Map title/selected detail to magenta `5`; focus/ranges/links/selection to cyan `6`; formula/cask/font to blue `4`/magenta `5`/yellow `3`; success/warning/error to green `2`/yellow `3`/red `1`; and secondary text to bright black `8`.
       - Set no explicit background and use no ANSI-256, RGB, or adaptive colors. The selected row uses cyan foreground plus reverse video rather than a background assignment.
       - Keep reverse video plus a `›` marker for selection and bold/underline/brackets for the active range.
       - Under `NO_COLOR`, remove every color while keeping warnings/focus/errors distinguishable through labels, borders, attributes, and text markers.
     - Keep relative-age formatting deterministic by injecting `now`.
   - Tests:
     - Golden snapshots with color disabled for every required state.
     - Width/height assertions for exact breakpoint boundaries, active type indicators, long Unicode descriptions, and first/middle/last centered-row behavior across odd/even/fewer/equal/empty capacities.
     - Structural style tests prove normal mode uses exactly the approved ANSI `1`–`8` roles with no background, and `NO_COLOR` leaves every app/text-input foreground and background unset.
     - Selected rows remain distinguishable through reverse video and test-visible markers.
   - Acceptance:
     - `go test ./internal/ui`
     - `go test ./...`
   - Dependencies:
     - Depends on Task 8.
   - Technical mitigation:
     - Avoid global color-profile mutation in parallel tests. Build styles from an explicit no-color flag and normalize golden output through the renderer configuration.

10. **Wire startup, refresh, opener, and Bubble Tea install handoff**
    - Files:
      - `internal/platform/tea.go`
      - `internal/platform/tea_test.go`
      - `cmd/brewnicle/main.go`
      - `cmd/brewnicle/main_test.go`
   - Changes:
     - Replace the bootstrap main stub with dependency wiring only.
     - Startup behavior:
       1. Resolve cache paths.
       2. Load and validate the active index.
       3. If valid, launch browsing immediately and enqueue one background refresh only when stale at startup.
       4. If missing, launch the foreground bootstrap state.
       5. If invalid, preserve it diagnostically and bootstrap; report both failures if rebuild also fails.
     - Use a cancellable root context and cancel in-flight refresh/git/HTTP work on quit.
     - Adapt homepage opening into a normal Bubble Tea command.
     - Adapt confirmed installation through `tea.ExecProcess`/the installed Bubble Tea equivalent:
       - Build the validated `*exec.Cmd` in `internal/platform`.
       - Let Bubble Tea release and restore the terminal.
       - Attach stdin/stdout/stderr.
       - Return an install-finished message from the callback.
       - Set running-install state before yielding and restore browsing on completion.
     - Do not capture Homebrew’s interactive output into an unbounded buffer.
     - Installation absence/failure must not affect the index or browsing.
     - Ensure signal/quit paths do not promote an incomplete refresh.
   - Tests:
     - Main wiring with injected cache root, store, refresh service, and action adapters.
     - Valid/fresh, valid/stale, missing, and corrupt index startup.
     - Bubble Tea install adapter returns the expected callback message without running a real install.
     - Quit cancellation reaches refresh.
   - Acceptance:
     - `go test ./cmd/brewnicle ./internal/platform ./internal/ui`
     - `go build ./cmd/brewnicle`
     - `go test ./...`
   - Dependencies:
     - Depends on Tasks 5–9.
   - Technical mitigation:
     - Verify the exact Bubble Tea version’s process-handoff API while implementing. Keep all version-specific `ExecProcess` handling in `internal/platform/tea.go` so API drift does not spread through the UI.

11. **Add user documentation and opt-in live compatibility tests**
   - Files:
     - `README.md`
     - `internal/catalog/integration_test.go`
     - `internal/history/integration_test.go`
   - Changes:
     - Document:
       - What “added” means and why it differs from `brew update`.
       - Current non-disabled catalog scope.
       - Formula/cask/font handling and unknown font dates.
       - First-run network, Git, time, bandwidth, and disk cost without promising exact figures.
       - Subsequent cached/offline behavior and 24-hour startup check.
       - All keybindings.
       - macOS/Linux support, required Git, optional Homebrew for browsing, and required Homebrew for install.
       - Cache locations via the OS user-cache directory.
       - `NO_COLOR` behavior.
       - Development and validation commands.
       - No hosted index, third-party taps, release automation, or speculative package-management features.
     - Add build-tagged `integration` tests that:
       - Fetch and normalize the live official formula/cask APIs.
       - Verify both official git remotes expose a default branch.
       - Do not clone full histories, modify Homebrew, or install packages.
     - Keep full history correctness in deterministic temporary-repository tests.
   - Acceptance:
     - `go test ./...`
     - `go test -tags=integration ./internal/catalog ./internal/history` when network access is explicitly available.
     - `go build ./cmd/brewnicle`
   - Dependencies:
     - Depends on all functional tasks.
   - Checkpoint:
     - The repository explains the product accurately and has an opt-in upstream compatibility check without making normal tests network-dependent.

12. **Run final validation and perform a read-only smoke test**
   - Files:
     - Modify only files implicated by validation failures; do not add CI/release/package-distribution files.
   - Changes:
     - Run formatting on all Go files.
     - Ensure direct dependencies are limited to the four approved libraries; inspect indirect dependencies as transitive requirements.
     - Run the complete suite under the race detector.
     - Run vet and build the command.
     - Exercise the TUI manually with a temporary prepared fixture index:
       - Wide, narrow, and too-small resize states.
       - Search, all time filters, package-type cycling, and centered selection at middle/boundary positions.
       - Help and install confirmation cancellation.
       - Missing homepage and missing Homebrew states.
     - If network/time permits, run the opt-in integration tests and a real read-only refresh. Do not run a real package installation as part of automated or smoke validation.
     - Record first-run history refresh duration/disk observations only as local evidence; do not hard-code them into product claims.
   - Acceptance:
     - `gofmt -w $(find cmd internal -name '*.go')`
     - `go mod tidy`
     - `go test ./...`
     - `go test -race ./...`
     - `go vet ./...`
     - `go build ./cmd/brewnicle`
     - Optional: `go test -tags=integration ./internal/catalog ./internal/history`
     - `git diff --check` by the supervising agent.
   - Dependencies:
     - Depends on Tasks 1–11.
   - Final checkpoint:
     - The tree builds, unit/fixture/golden tests pass, vet and race checks pass, default tests require no network, and no real Homebrew mutation occurs during validation.

## Files to Modify

- `.gitignore` - only if implementation creates an additional local cache artifact not already covered; otherwise leave unchanged.
- `cmd/brewnicle/main.go` - evolve the initial compilable entry point into final dependency wiring.

The approved design specification should not be modified unless implementation exposes a genuine contradiction requiring supervisor approval.

## New Files

- `go.mod` - module declaration and four approved direct dependencies.
- `go.sum` - reproducible dependency checksums.
- `README.md` - usage, data semantics, first-run cost, keys, platform support, and development instructions.
- `cmd/brewnicle/main_test.go` - startup and dependency-wiring tests.
- `internal/domain/package.go` - package kinds and domain record.
- `internal/domain/filter.go` - ranges, search, filtering, and sorting.
- `internal/domain/filter_test.go` - domain boundary tests.
- `internal/catalog/client.go` - bounded cancellable HTTP client.
- `internal/catalog/types.go` - minimal API response structs.
- `internal/catalog/normalize.go` - validation, disabled exclusion, and font classification.
- `internal/catalog/client_test.go` - catalog tests.
- `internal/catalog/testdata/formula.json` - representative formula fixture.
- `internal/catalog/testdata/cask.json` - representative cask fixture.
- `internal/catalog/integration_test.go` - opt-in live API compatibility test.
- `internal/history/path.go` - supported path-family matching.
- `internal/history/parser.go` - machine-readable git-log parsing.
- `internal/history/scanner.go` - bulk earliest-event scanning and resolution.
- `internal/history/cache.go` - app-owned repo lifecycle.
- `internal/history/git.go` - cancellable Git runner.
- `internal/history/path_test.go` - path-family tests.
- `internal/history/parser_test.go` - parser tests.
- `internal/history/scanner_test.go` - temporary-repository history tests.
- `internal/history/cache_test.go` - cache/ref promotion tests.
- `internal/history/integration_test.go` - opt-in remote default-branch check.
- `internal/store/schema.go` - SQLite schema and version.
- `internal/store/store.go` - load and validation.
- `internal/store/publish.go` - atomic temporary-DB publication.
- `internal/store/store_test.go` - schema/load tests.
- `internal/store/publish_test.go` - replacement/failure tests.
- `internal/refresh/paths.go` - OS cache location resolution.
- `internal/refresh/service.go` - catalog/history/store orchestration.
- `internal/refresh/progress.go` - refresh phase messages.
- `internal/refresh/service_test.go` - orchestration tests.
- `internal/refresh/paths_test.go` - path safety tests.
- `internal/platform/runner.go` - injected process boundary.
- `internal/platform/open.go` - homepage validation and opener execution.
- `internal/platform/install.go` - installation command construction.
- `internal/platform/tea.go` - Bubble Tea terminal handoff adapter.
- `internal/platform/actions_test.go` - direct-argv action tests.
- `internal/platform/tea_test.go` - process-handoff adapter tests.
- `internal/ui/model.go` - application state.
- `internal/ui/messages.go` - Bubble Tea messages.
- `internal/ui/keys.go` - key map.
- `internal/ui/update.go` - interaction state machine.
- `internal/ui/actions.go` - injected action commands.
- `internal/ui/view.go` - top-level responsive rendering.
- `internal/ui/view_wide.go` - split-pane rendering.
- `internal/ui/view_narrow.go` - narrow list/detail rendering.
- `internal/ui/styles.go` - terminal-native styles.
- `internal/ui/format.go` - wrapping, dates, and detail formatting.
- `internal/ui/model_test.go` - update/state tests.
- `internal/ui/view_test.go` - rendering and breakpoint tests.
- `internal/ui/testdata/golden/*.golden` - stable no-color UI snapshots.

## Dependencies

- Task 1 establishes the module and domain contract used by every later task.
- Tasks 2 and 3 can be implemented after Task 1, but one writer should complete them sequentially.
- Task 4 depends on the Task 3 scanner/ref contract.
- Task 5 depends only on the domain model and may follow Task 4 to keep the writer focused.
- Task 6 integrates Tasks 2–5 and forms the headless refresh milestone.
- Task 7 supplies action contracts consumed by the TUI.
- Task 8 depends on domain, refresh, and platform-facing interfaces.
- Task 9 depends on the stable UI model from Task 8.
- Task 10 wires all completed components into the executable.
- Tasks 11 and 12 follow the complete functional implementation.

Recommended implementation checkpoints:

1. **Domain checkpoint:** Tasks 1–2; module, filters, and catalog normalization pass.
2. **History checkpoint:** Tasks 3–4; deterministic first-add resolution and safe app-owned git caching pass.
3. **Headless index checkpoint:** Tasks 5–6; refresh builds and atomically reloads a complete index.
4. **Interaction checkpoint:** Tasks 7–9; user workflows and responsive renderings pass without real external actions.
5. **Executable checkpoint:** Tasks 10–12; wiring, docs, integration checks, race, vet, and build pass.

## Risks

- **Git history cost:** `--filter=blob:none` still requires extensive commit/tree history and may lazily fetch trees. Show phase-level progress and document real first-run cost; do not promise a shallow or instant bootstrap.
- **Git output parsing:** Human-readable log formats and rename heuristics are fragile. Use explicit separators, validate all fields, disable rename detection, and abort rather than publish questionable dates.
- **Remote branch drift:** Do not assume `main` forever. Discover and validate symbolic remote HEAD, then scan a dedicated candidate/last-good ref.
- **Partial-clone compatibility:** Older Git/remote behavior may reject filters. Retry without the unsupported filter while preserving clear diagnostics.
- **SQLite replacement:** WAL files and open handles undermine single-file atomic rename. Build with DELETE journaling, close/reopen/validate, remove sidecars, and rename only a sibling file on supported POSIX platforms.
- **Corrupt-index handling:** Preserve the original file under a diagnostic suffix before rebuilding; do not repeatedly overwrite diagnostic evidence.
- **Bubble Tea API/version drift:** Keep `ExecProcess` adaptation isolated in `internal/platform/tea.go` and verify compile/runtime behavior against the pinned version.
- **Interactive Homebrew output:** Never capture unbounded install output. Yield the terminal and attach standard streams, then restore the TUI via callback.
- **Terminal theme compliance:** Lip Gloss and Bubbles defaults can accidentally emit fixed RGB/ANSI-256 colors or backgrounds. Restrict normal-mode styling to the approved terminal ANSI `1`–`8` roles, set no background, clear dormant component defaults, and validate a fully colorless `NO_COLOR` mode structurally.
- **Unicode width and resizing:** Byte/string lengths do not equal terminal cell widths. Use Lip Gloss width measurement and test long/wide Unicode at exact breakpoints.
- **Upstream schema drift:** Minimal JSON structs tolerate added fields, while status, size, required identifiers, and canonical formats remain validated. Live tests stay opt-in.
- **Unknown font dates:** Current nested font-cask paths are supported and live-validated. The deleted historical fonts repository is intentionally not required; fonts without reachable cask history still remain unknown and appear only in `all`.
- **Scope expansion:** Do not add third-party taps, dependency views, installed-package management, release automation, a hosted index, or a real-install smoke test.