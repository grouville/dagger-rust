# Rust

Cargo-compatible checks and build artifacts for a Cargo workspace, through
ordinary Dagger commands.

## Usage

In the workspace root, `dagger.toml`:

```toml
[modules.rust]
source = "https://github.com/grouville/dagger-rust"

[modules.rust.settings]
locked = true
cacheKey = "my-project"
```

Write the source with an explicit `https://`: a scheme-less ref makes the engine probe
HTTPS and SSH with `git ls-remote` on every command (~400ms) before it reads the lock.

```sh
dagger check rust:check                 # cargo check
dagger check rust:fmt rust:clippy rust:test
dagger check                            # all of the above, plus generated-artifact drift
dagger generate -y                      # cargo build; publish artifacts under target/dagger
dagger call rust artifacts export --path ./dist
```

Every entrypoint takes the project as a contextual input, so an unchanged tree
is a cache hit on any command. Each action (check, clippy, test, build) owns a
source mirror and an intermediate build directory in locked cache volumes, so
`dagger check` runs them concurrently; final artifacts are immutable results.
The generator owns `target/dagger` only. With `locked = false` it also
publishes the `Cargo.lock` Cargo produced.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `image` | pinned `rust` Debian bookworm amd64 image | toolchain image; any bookworm/amd64 Rust image works, the pinned packages below assume bookworm |
| `cacheKey` | `rust` | prefix of the project's cache volumes; use one per project |
| `locked` | `false` | pass `--locked` and require an up-to-date `Cargo.lock` |
| `extraPackages` | `[]` | extra Debian packages for build scripts (`cmake`, `pkg-config`, `libssl-dev`, ...); gcc and make are always present |
| `cargoProfile`, `target`, `features`, `allFeatures`, `noDefaultFeatures` | unset | forwarded to every Cargo invocation |

A root `rust-toolchain` / `rust-toolchain.toml` is honored: the selected
toolchain and the components a check needs (clippy, rustfmt) are installed in
a cached layer keyed by those files only.

## Notes

- Invoke from the workspace root.
- Linux/amd64 only; artifacts are Linux/GNU outputs.
- Cargo >= 1.91 keeps intermediate state and final outputs apart
  (`build.build-dir`); older toolchains use one target directory per action.
  The module never changes the project's toolchain.
- `.git`, `target`, `dagger.toml` and `dagger.lock` are excluded from the
  source Cargo sees. `Cargo.lock`, `.cargo/config.toml`, build scripts and
  sources are inputs.
- The generator rejects symlink artifacts and workspaces with no final
  artifacts.
- Disk: one intermediate build directory per action per project.
