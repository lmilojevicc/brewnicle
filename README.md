# Brewnicle

Brewnicle is a Go/Charm terminal UI for discovering packages recently added to the official Homebrew catalog. It shows current installable formulae, casks, and font casks with descriptions and upstream first-add dates.

## What “added” means

`brew update` advertises only the difference from the previous local update. Homebrew does not retain that advertisement history. Brewnicle instead combines the current non-disabled [formula](https://formulae.brew.sh/api/formula.json) and [cask](https://formulae.brew.sh/api/cask.json) catalogs with the earliest matching add commit reachable in the official `homebrew-core` and `homebrew-cask` Git histories.

Only current, non-disabled packages are listed. Removed packages and third-party taps are out of scope. The `all` view contains every enabled formula and cask returned by the current official catalogs, so its count changes as Homebrew changes. A font is a cask whose token starts with `font-`; it is shown once as `font` and installed with `--cask`. If a package’s earlier history cannot be resolved, its date is unknown and it appears only under `all`.

History scanning recognizes Homebrew's current strict package layouts, including `Formula/lib/<name>.rb` formulae and `Casks/font/font-<bucket>/<token>.rb` font casks, in addition to legacy and single-character bucket layouts. Live compatibility tests fail if enabled catalog entries move to an unsupported path family.

## First run and cache

The first run downloads both API catalogs and app-owned, blob-filter-requested Git history caches. As soon as the catalogs arrive, Brewnicle switches to a provisional `all` view so current packages can be searched, opened, and installed while exact first-add dates index in the background. Date-bounded ranges are disabled and dates are explicitly marked as indexing until that exact history pass finishes. If indexing fails, catalog browsing remains available and `r` retries it.

Homebrew’s histories are large: initialization can require substantial network transfer, disk space, and time even though source blobs are not requested. Git servers or clients may ignore filtering. Brewnicle reports phase-level progress and does not promise an exact size or duration. A completed full clone is validated and atomically checkpointed as `core.git.bootstrap` or `cask.git.bootstrap` before its CPU-intensive scan. If the app exits or scanning fails, the next run reuses that download instead of cloning it again. A pending clone is never treated as completed history and never publishes incomplete package rows or refs.

The app never modifies Homebrew’s own taps. It stores an SQLite index and bare Git caches under the OS user-cache directory (`~/Library/Caches/brewnicle` on macOS, normally `$XDG_CACHE_HOME/brewnicle` or `~/.cache/brewnicle` on Linux). Provisional catalog rows stay in memory only. After both exact history scans validate, Brewnicle atomically publishes the complete SQLite generation and promotes the validated repositories. Later starts render the cached index immediately. At startup only, an index at least 24 hours old refreshes in the background; `r` forces refresh. A failed refresh leaves the prior index usable. The app can browse offline after a successful bootstrap.

Schema-v1 indexes remain visible while Brewnicle performs one final background CPU-intensive full-history scan to build its incremental history index; this reuses the existing Git caches rather than downloading the repositories again. Full scans and ranges use Git's full reachable-history traversal with combined merge diffs so merged side-branch and merge-resolution additions are retained without inventing merge timestamps. The SQLite generation then stores complete earliest-add aggregates plus the exact published core/cask commit cursors. Subsequent same-tip refreshes perform no history scan, and ordinary updates scan exactly the commits between the published cursor and the new official tip. A rewritten, missing, shallow, corrupt, or algorithm-incompatible repository falls back to a bounded full rebuild of only that repository.

SQLite cursors are authoritative; Git fetched/published refs are repairable object pins. A cache-root file lock serializes refreshes from multiple Brewnicle processes. When another process owns it, the TUI reports that it is waiting and reloads the winning index after acquiring the lock. Failures before database rename preserve the prior generation; directory-sync, ref-reconciliation, or lock-release failures after rename are warnings because the new generation is already active. Legacy `candidate` and `last-good` refs may retain orphaned Git objects on disk after rewritten history until a later cleanup release. UI filters never change backend refresh scope.

## Keys

| Key | Action |
|---|---|
| `↑`/`↓`, `j`/`k` | move selection |
| `1`…`5` | `7d`, `30d`, `90d`, `1y`, `all` |
| `tab` / `shift+tab` | cycle ranges |
| `f` / `F` | cycle package type forward/back: all, formula, cask, font |
| `/` | search name and description |
| `esc` | leave/clear search or close modal |
| `o` | open selected homepage |
| `i` | show install confirmation |
| `r` | refresh |
| `enter` | confirm; toggle details on narrow terminals |
| `?` | help |
| `q`, `ctrl+c` | quit when search or install confirmation is not active |

Search and install confirmation own text keys, including `q` and `ctrl+c`; leave them with `esc`/`enter` or the displayed confirmation controls. The too-small screen keeps its explicit quit control. The default range is `30d` and the default package type is `all`. During first-run date indexing, the range is temporarily forced to `all`; date-bounded ranges become available when exact dates finish. Package type, time range, and search filters combine together. The selected package row stays vertically centered while browsing when possible; the first and last pages clamp without blank padding. If `brew` is unavailable, browsing and homepage actions remain usable while installation is visibly disabled. Installation never starts without confirmation. Formulae run `brew install NAME`; casks and fonts run `brew install --cask TOKEN`. Commands use direct argument vectors, never a shell. Bubble Tea yields the terminal to Homebrew for interactive output and restores the TUI afterward.

Brewnicle uses a vivid theme built only from the terminal’s ANSI palette, so the terminal theme controls the actual hues. Magenta marks titles and selected package names; cyan marks focus, active ranges, links, and the reverse-video selected row; formulae are blue, casks magenta, and fonts yellow; success is green, warnings are yellow, errors are red, and secondary help is bright black. Brewnicle never uses RGB, ANSI-256, adaptive colors, or a painted background. `NO_COLOR` removes every color while preserving bold, underline, reverse video, brackets, labels, and the `›` marker.

## Requirements and usage

- macOS or Linux
- Git for first bootstrap and refresh
- Homebrew only for installation (browsing works without it)

```sh
go run ./cmd/brewnicle
```

## Development

Go **1.25.0 or newer** is required.

```sh
go mod tidy
gofmt -w $(find cmd internal -name '*.go')
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/brewnicle ./cmd/brewnicle
```

Live compatibility tests are opt-in and never install packages or clone full histories:

```sh
go test -tags=integration ./internal/catalog ./internal/history
```

### Isolated fixture smoke test

This creates a fixture through the real store API in a temporary cache and launches the actual TUI without touching your normal cache or requiring network refresh:

```sh
go build -o /tmp/brewnicle-smoke ./cmd/brewnicle
TMP_HOME="$(mktemp -d)"
HOME="$TMP_HOME" XDG_CACHE_HOME="$TMP_HOME/cache" \
  BREWNICLE_SMOKE_CACHE="$TMP_HOME/cache/brewnicle" \
  go test ./cmd/brewnicle -run '^TestWriteSmokeFixture$' -count=1
HOME="$TMP_HOME" XDG_CACHE_HOME="$TMP_HOME/cache" /tmp/brewnicle-smoke
```

Exercise wide (≥90×16), narrow (at least 50×12 but not wide, including wide-but-short terminals), and too-small views; search, ranges, package-type cycling, centered selection near the middle and boundaries; help; homepage-unavailable feedback; and install confirmation cancellation. Do not confirm a real install as part of validation.

Brewnicle intentionally has no hosted index, telemetry, dependency management, release automation, or package upgrade/uninstall features.
