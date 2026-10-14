# Rust

Cargo-compatible checks and build artifacts for a Cargo workspace, through
ordinary Dagger commands.

## Usage

In the workspace root, `dagger.toml`:

```toml
[modules.rust]
source = "https://github.com/grouville/dagger/modules/rust@rust-module"

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
| `cacheKey` | `rust` | project cache namespace; use one per project |
| `nativeCache` | `false` | experimental native compiler snapshots for check, test and generated artifacts; requires the prototype engine described below |
| `locked` | `false` | pass `--locked` and require an up-to-date `Cargo.lock` |
| `extraPackages` | `[]` | extra Debian packages for build scripts (`cmake`, `pkg-config`, `libssl-dev`, ...); gcc and make are always present |
| `cargoProfile`, `target`, `features`, `allFeatures`, `noDefaultFeatures` | unset | forwarded to every Cargo invocation |

A root `rust-toolchain` / `rust-toolchain.toml` is honored: the selected
toolchain and the components a check needs (clippy, rustfmt) are installed in
a cached layer keyed by those files only.

## Experimental native compiler cache

The `experiment/native-build-test` branch can run the normal `rust:check`,
`rust:test` and `rust:build` entrypoints with `nativeCache = true`. It requires
an experimental engine with native cache hints and `Directory.checkpoint`;
released engines do not provide these APIs. The reference engine branch is
[`experiment/persistent-native-cache-hints`](https://github.com/grouville/dagger/tree/experiment/persistent-native-cache-hints).

Compiler outputs and rustc incremental state are immutable Dagger directories.
The engine discovers the latest state by project namespace and command, while
compiled-result reuse remains keyed by the compiler inputs. Compiler state is
kept before tests execute, so a test failure does not discard a successful build.

Tests retain Cargo's unit, integration, custom harness and documentation commands,
including their working directories, arguments and environment. They see the
whole workspace, including runtime data outside crate directories, and execute
in Cargo order so filesystem changes reach later commands. Test execution has
its own inputs: a runtime-only file edit can reuse every compiler result and
still fail a test. Configured target runners are rejected by this prototype.

`dagger generate -y` publishes Cargo's final workspace binaries and libraries
under `target/dagger`. It disables rustc incremental compilation to keep these
generated files reproducible across engines. The `nativeBuild` API retains
incremental state for edited development builds. These modes have separate
cache namespaces; the performance target for normal generated builds remains
unfinished.

Eight paired edited test commands on a warm engine take 1.075 s median after
batching test environment inputs, versus 1.236 s for individual variable
operations and 0.346 s for Cargo. All eight pairs improve. Processes remain fresh;
this reduces graph preparation. The performance target remains open. Native
state survives clean engine restart, and explicit snapshot transfer executes
correctly on a second engine.

The `experiment/native-checkpoint-snapshot-key` branch additionally requires
an engine with `Directory.checkpoint(snapshotIdentity: true)`. Retaining a
completed immutable compiler snapshot uses its committed identity, path and
platform instead of reading its contents again. This keeps independent
snapshot ownership and native transfer; separately produced equal-content
snapshots are not deduplicated at checkpoint creation.

Eight paired shared edits in a 102-action workspace take 1.385 s median with
this mode, versus 1.443 s with content checkpointing. All eight pairs improve.
Leaf edits show no established gain (0.869 s versus 0.862 s). The local host had
some contention, and the Cargo +0.5-second target remains unfinished.

The supported subset is locked Linux/amd64 workspaces with path dependencies,
no build scripts or procedural macros, and no extra system packages. Registry
dependencies and broader Cargo compatibility remain work in progress. Formatting
and Clippy still use the existing Cargo backend.

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
