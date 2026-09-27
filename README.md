# brewnicle

A fast interactive terminal UI for discovering newly added official Homebrew packages with upstream first-add dates, full-text search, filtering, and installation.

When you run `brew update`, Homebrew announces packages added since your last local update, but discards that history as soon as the terminal scrolls. Brewnicle pairs the official Homebrew formula and cask catalogs with upstream Git commit histories to provide a searchable chronicle of packages added over the last 7 days, 30 days, 90 days, 1 year, or all time.

---

## Features

- **Dual-Pane Bordered Layout:** Clean terminal UI featuring a boxed header, split list/detail body, and status footer. Automatically adapts to narrow terminals with an in-place details view.
- **Instant Search:** Press `/` to filter packages interactively across names and descriptions.
- **Time-Range Filtering:** Switch across `7d`, `30d`, `90d`, `1y`, or `all` using keys `1`–`5` or `Tab`.
- **Package Kind Filtering:** Cycle between formulae, casks, and fonts (`f` / `F`).
- **Catalog-First Browsing:** Search, inspect, and install packages immediately on first launch while exact upstream commit dates index in the background with live elapsed progress.
- **Confirmed In-App Installation:** Review the exact install command in the detail pane and trigger installation with `i`. Installation requires explicit confirmation (`Enter` or `y`) and yields the terminal to Homebrew for interactive output.
- **Homepage Opening:** Press `o` to launch the selected package's upstream homepage in your default browser.
- **Offline SQLite Cache:** Stores package metadata and Git history timestamps locally, enabling instant launches and full offline browsing after bootstrap.
- **ANSI Palette & NO_COLOR:** Uses your terminal's native ANSI colors for consistent theme integration. Fully respects the [`NO_COLOR`](https://no-color.org) standard.

---

## Prerequisites

- **macOS** or **Linux**
- **Go >= 1.25.0** (if installing or compiling from source)
- **Git** on `PATH` (used for background history indexing)
- **Homebrew** on `PATH` (optional; required for package installation, while browsing and opening homepages work without it)

---

## Installation

Install the latest binary using Go:

```sh
go install github.com/milo/brewnicle/cmd/brewnicle@latest
```

Or run directly from source:

```sh
go run ./cmd/brewnicle
```

*(Note: `brewnicle` accepts no CLI flags.)*

---

## Keybindings

| Key | Action |
| --- | --- |
| `↑` / `k`, `↓` / `j` | Move selection up / down |
| `1` – `5` | Select time range (`7d`, `30d`, `90d`, `1y`, `all`) |
| `Tab` / `Shift+Tab` | Cycle time range forward / backward |
| `f` / `F` | Cycle package type forward / backward (`all`, `formula`, `cask`, `font`) |
| `/` | Search package name and description |
| `Esc` | Clear search, dismiss modal, or exit help overlay |
| `o` | Open package homepage in browser |
| `i` | Open install confirmation modal |
| `Enter` | Confirm install (in modal); toggle details on narrow terminals |
| `y` / `n` | Confirm / cancel install in modal |
| `r` | Refresh catalog and history cache (or retry if indexing failed) |
| `?` | Toggle help overlay |
| `q`, `Ctrl+C` | Quit (when search or confirmation modal is not active) |

---

## Environment & Caching

### Environment Variables

- `NO_COLOR`: When set to any value (per [no-color.org](https://no-color.org)), disables ANSI color styling while retaining text formatting, borders, and selection markers.

### Cache Directory

Brewnicle stores its SQLite index (`index.db`), bare Git history caches (`git/`), and lockfiles (`refresh.lock`) under the OS user-cache directory:

- **macOS:** `~/Library/Caches/brewnicle`
- **Linux:** `$XDG_CACHE_HOME/brewnicle` (or `~/.cache/brewnicle` if unset)

The cache root is validated against symbolic links and never modifies Homebrew's own taps or files.

---

## Intentional Boundaries

- **Official Catalog Only:** Tracks official `homebrew-core` and `homebrew-cask` packages. Third-party taps and removed/deprecated packages are out of scope.
- **Read-Only Catalog Browser:** Brewnicle is a discovery tool and does not manage local package updates, upgrades, uninstalls, or dependency trees.
- **No Telemetry / No Daemon:** Brewnicle contains no telemetry, phones home to no third-party servers, and runs no background daemon.

---

## Development & Architecture

For development workflows, build instructions, test suites (including unit, race, integration, and isolated fixture smoke tests), and deep architecture notes, see [docs/development.md](docs/development.md). Additional architectural specifications and design documents are located in [docs/](docs/).
