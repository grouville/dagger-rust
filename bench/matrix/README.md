# Real-project matrix: native Cargo vs the module, same flows

`projects.toml` names each fixture's leaf-crate and core-crate edit files (and per-project
cargo flags). `mflow.sh <project> <native|dagger> <exact|leaf|core|checkall> <n>` runs one
flow on one side; `pairs.py` alternates the two sides with the same edit id per pair
(`--offset` to avoid re-using edit contents the module already cached); `edit.py <root> file
<path> <n>` appends a `// bench-<n>` marker so cargo rebuilds that crate.

Setup per project: clone at the pinned commit; a native container from the module's pinned
Rust image with `rustup component add clippy rustfmt` and the source copied to `/src`; a
`dagger.toml` with `locked = true` and a project `cacheKey`. Engine pinned with
`DAGGER_ENGINE=container://...`, no Cloud credentials, GC-reserve fix or GC off.

Results are in `matrix-results.md` (medians, ms).
