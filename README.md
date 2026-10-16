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
dagger check rust:fmt rust:clippy rust:packages/tests/test
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
test and build entrypoints with `nativeCache = true`. It requires
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

Native test plans retain the selected `rustdoc` executable from the pinned image.
Rustup selects that binary once during capture; replay uses it directly for each
documentation test. This preserves test execution and avoids repeated toolchain
selection. Old automatic test plans refresh once, and explicit older plans remain
usable. Rustdoc overrides in Cargo configuration are rejected; image environment
overrides are retained.

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
# Experimental test collections

The `experiment/test-collection-batched-inputs` branch exposes packages and their
individual tests as two collection dimensions, matching the Go module's selection
levels. It currently requires `nativeCache = true`.

```sh
dagger check rust:packages/tests/test
dagger check 'dag://?check=rust/packages/tests/test&rust-package=my_crate'
dagger check 'dag://?check=rust/packages/tests/test&rust-package=my_crate&rust-test=src/lib.rs::tests::example'
```

Test names come from the compiled Rust test harness, including generated and
configured tests. Unit and integration tests use their target source path and
function name as a key. Documentation tests remain one entry per target. Custom
test harnesses are not supported by this collection prototype.

Package discovery is shared. Compiler plans and test discovery are retained per
package, so selecting one package also excludes compilation errors in other
packages. Listing tests compiles their harnesses when needed. Each package's selected
tests run in one container; a partial selection keeps the captured test command,
environment and working directory. Different packages have separate filesystem
views. The workspace command preserves the existing command order and shared
filesystem effects across packages:

```sh
dagger api call rust test --source .
```

That function remains callable directly and no longer registers a second test
check alongside the collection. Compiler outputs and incremental state remain
native immutable Dagger snapshots. This branch is an experiment for comparing
selection granularity and execution overhead; the existing selected module stays
on its workspace batch implementation.

This branch combines compilation and test discovery in one compiler-module call
per package. It reuses the loaded compiler plan and native directories; listing
and executing tests still use separate processes. At 64 packages, wcprof records
193 process starts instead of 257. Compiler cache keys and worker binaries are
unchanged.

| Packages, eight tests each | Separate preparation | Combined preparation | Workspace batch | Cargo |
| --- | ---: | ---: | ---: | ---: |
| 1 | 0.90 s | 0.77 s | 0.88 s | 0.22 s |
| 16 | 2.73 s | 2.49 s | 0.88 s | 0.24 s |
| 64 | 10.20 s | 8.15 s | 0.87 s | 0.31 s |

Four balanced pairs per size, with a fresh source edit and CLI process each time.
The engine and compiler caches are warm; telemetry and normal GC stay enabled.
All four pairs improve at one package, and three of four at each larger size.
One 64-package pair is 47 ms slower. Allocated bytes fall from 8.06 to 6.90 GB
at 64 packages; these are cumulative allocations, not peak memory.

Replay and worker race checks pass. Integration checks cover partial test
selection, selected test failures, excluding an invalid crate, repair, and the
ordinary workspace API. Fine collections still cost much more than the workspace
batch and remain experimental. The
[measurement record](https://github.com/grouville/dagger/blob/experiment/rust-native-cache/hack/rust-cache/NATIVE_TEST_COLLECTIONS.md)
includes all samples and separate profiles.

This experiment also applies the generated dispatcher from the
[Go codegen change](https://github.com/grouville/dagger/commit/af040470362736cf72f56d7a9f5365564fa2a751).
The compiler helper reads its function metadata and argument values in one
request, instead of 28 requests for its 12 supplied arguments. No worker or
compiler-cache code changes. Regenerating with an unpatched code generator will
replace this experimental dispatcher.

At 64 packages, complete edited collections measure 8.60 → 8.25 seconds in four
pairs and 8.92 → 8.10 seconds in a separate eight-pair confirmation. Ten of the
twelve pairs improve. Wcprof records 5,097 → 3,369 engine queries with the same
193 workload starts. The ordinary workspace API stays flat at 0.92 → 0.93 seconds
over six pairs. These gains are separate from the earlier preparation comparison.
Fine collections remain experimental; the workspace batch is still much faster.

