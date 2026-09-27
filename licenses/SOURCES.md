# Supplemental dependency notices

Release archives and the Homebrew formula preserve dependency license files and
embedded legal comments from the compiled Go sources. Brewnicle's MIT license
does not relicense those dependencies.

These additional texts cover Unicode data used by `rivo/uniseg` (Unicode 15)
and `clipperhouse/uax29` (Unicode 17), including data used by terminal-width code:

- `Unicode-DFS-2016.txt` and `Unicode-3.0.txt`: SPDX License List v3.27.0,
  commit `d46e94e2c78ceede1cfc63cfa0396472d2798d4c`, `text/` directory at
  <https://github.com/spdx/license-list-data>.
- `Unicode-15.0.0-NOTICE.txt`: <https://www.unicode.org/Public/15.0.0/ucd/ReadMe.txt>.
- `Unicode-17.0.0-NOTICE.txt`: <https://www.unicode.org/Public/17.0.0/ucd/ReadMe.txt>.

`HSLuv-LICENSE.txt` preserves the upstream MIT notice for HSLuv code embedded in
`lucasb-eyer/go-colorful/hsluv.go`, which links to HSLuv but does not reproduce
its copyright notice. Source: <https://raw.githubusercontent.com/hsluv/hsluv-go/34f81136a322719b960c333f4767e9764de6328d/LICENSE.txt>.
SHA-256: `092cd62d642081a92e81c12da3ad7659e18b38fd1ac7e273d5fcec265d192f3b`.

Review these supplements when updating Unicode-related dependencies. Generated
`licenses/MODULES.txt` records the exact modules included in each distribution;
`SOURCE_NOTICES.txt` files preserve source-level attributions in addition to
module license files. Collection fails when a compiled module has no license.
