# Releasing

Pushing a stable `vMAJOR.MINOR.PATCH` tag automatically publishes a GitHub release.
Do not push a tag merely to test this setup. Prerelease suffixes, build metadata,
and leading-zero version components are rejected.

## Before tagging

1. Merge the intended commit to `main` and wait for **CI** to pass.
2. Review dependency changes and their notices, including the Unicode supplements
   in [`licenses/`](../licenses/SOURCES.md). MIT covers Brewnicle, not all bundled code.
3. Confirm the intended version and commit, then create and push the version tag.
   Never move a published tag or replace a published asset.

The release workflow verifies main ancestry and reruns Linux/macOS CI at the tag.
It builds all four targets, validates archive contents and executable headers,
then transfers them to a separate publication job. Only that job has
`contents: write`. It creates a draft, uploads all assets, downloads them again,
checks hashes, and publishes the complete draft. No PAT, npm, OIDC, signing,
notarization, or deployment credentials are needed.

A publication failure may leave a draft. Inspect the failed run and draft; delete
only the incomplete draft before rerunning. The workflow refuses to overwrite an
existing release. Do not manually publish an incomplete draft.

## Asset contract

Each release has exactly these five assets (substitute the actual tag):

```text
brewnicle_v1.2.3_darwin_amd64.tar.gz
brewnicle_v1.2.3_darwin_arm64.tar.gz
brewnicle_v1.2.3_linux_amd64.tar.gz
brewnicle_v1.2.3_linux_arm64.tar.gz
SHA256SUMS
```

Targets are macOS and Linux, each on AMD64 or ARM64; Windows is unsupported.
Builds use `CGO_ENABLED=0`, `-trimpath`, and `-buildvcs=false`. macOS binaries are
not Developer ID signed or notarized. Cross-build/header verification is not a
substitute for runtime testing on every supported machine.

Archives have no enclosing directory. They contain an executable `brewnicle`,
Brewnicle's `LICENSE`, and `licenses/`: the union of compiled dependency notices
across all four targets, source-level legal comments, Go/toolchain notices, the
module inventory, and supplemental Unicode/HSLuv texts. The collector rejects replaced or
unversioned modules and modules with no license file. Archive timestamps/owners are normalized;
byte reproducibility still requires the same Go toolchain and inputs.

`SHA256SUMS` has one lowercase SHA-256 plus two spaces and the filename per archive.
Checksums detect corruption; they are not independently signed attestations.
Download from <https://github.com/lmilojevicc/brewnicle/releases> and verify hashes
before extracting.

## Local packaging check (no publication)

From the repository root with Go and Python 3.10+ installed:

```sh
python3 -m unittest discover -s scripts -p 'test_*.py' -v
TMP_RELEASE="$(mktemp -d)"
python3 scripts/release.py v0.0.0 "$TMP_RELEASE/assets"
(cd "$TMP_RELEASE/assets" && shasum -a 256 -c SHA256SUMS)
```

The output directory must not already exist. `v0.0.0` here is only a local filename
input: this command neither creates a tag nor uploads anything. The program has
no version flag; do not launch it as a packaging smoke check because startup may
begin network indexing.

## Homebrew propagation

The separate <https://github.com/lmilojevicc/homebrew-tap> initially provides only
`brew install --HEAD lmilojevicc/tap/brewnicle`. Its hourly/manual updater reads
this repository's latest published stable release via the public API, verifies
all four archives and hashes, then commits only `Formula/brewnicle.rb` using the
tap's own `GITHUB_TOKEN`. No release means no change. Stable installation becomes
available only after the first successful updater commit; HEAD remains available.

The updater rejects downgrade writes and same-version mutations. Authentication,
rate-limit, malformed metadata, unexpected hosts, missing assets, unsafe archives,
and checksum failures stop the update. It tests itself and checks Ruby syntax
before committing because bot-token pushes do not trigger another Actions run.
Scheduling is best-effort (GitHub may delay runs or disable schedules on inactive
repositories); maintainers can dispatch the updater manually. Tap branch policy
must allow this bot commit. No other formula is managed by the updater.
