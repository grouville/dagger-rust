# Experimental native compiler cache

The `compiler` dependency contains the tested Rust replay backend, extracted
from the Dagger engine experiment. It shares the module's existing API
connection. Compiler outputs and rustc incremental state are immutable native
directories; the cache consists of a manifest, batch tokens and Directory
fields. No Cargo compiler cache volume is used in this path.

`captureCheckPlan` records a locked, path-only workspace's Cargo check graph.
Pass that File as `compilerPlan` and pass the previous `nativeCheck.cache` to
the next `nativeCheck`'s `previous` argument. This keeps the Rust constructor
stable across edits. `compilerCache` remains available for the normal `check`
entrypoint, which uses this backend when a plan is configured. The normal
Cargo backend remains the default while automatic plan/cache discovery and
support for registry dependencies, build scripts, procedural macros and tests
are implemented. Published artifacts retain their existing reproducibility
policy.

`restoreCompilerCache` reconstructs transferred metadata with native Directory
objects on the receiver. Local numeric object IDs are not a portable cache.
The module uses its own public cache/result types rather than exposing types
owned by its implementation dependency.

The model, replay, worker and compiler-wrapper tests pass. The real Rust
entrypoint passes cold compilation, a source edit with three of four actions
reused, reversion with all four reused, and transfer of native cache fields to
a separate engine followed by another edit. Invalid Rust fails without
publishing a new cache. These tests currently cover a path-only workspace.

The Directory-edit tests require the engine's experimental fix for
`Directory.withNewFile`: it carries unchanged imported hashes into the child
snapshot and invalidates the written path. The standalone regression fails on
main without the patch. The patch leaves the filesync hash formatter unchanged.

Host imports are snapshots within one client session. Testing actual host edits
must use fresh command sessions; importing the same path again in one session
returns the original snapshot. The integration driver's `check` stage runs one
real import and compilation per command, retaining native objects on the engine.
Its local object IDs are acceleration handles for that engine, not a portable
remote index.

Five alternating edited-command pairs measured the original and optimized
native module at 1.154 s and 0.845 s median; native Cargo check took 0.174 s.
The change removes a Go runtime invocation that only wrapped cache fields,
selects compiler result fields together, and passes the previous native cache
object directly to the check. Each measured invocation edits source and reuses
three unchanged crates. The toolchain and engine caches are warm; each command
has a fresh client. Telemetry stays enabled with a local receiver, and wcprof is
disabled during timing. These are local prototype measurements through
`api with-session`, not the completed ordinary `dagger check` workflow or
production remote-engine latency.

The module generator currently forces SDK beta.12. The compiler module retains
beta.17 through a version replacement because replay requires the separate
`core` bindings. The local integration engine's CLI, session helpers and builtin
generator also had to be built from the engine's source. Its old embedded CLI
advertised beta.16 and failed nested calls against the beta.17 engine.

No upstream PR is open for this integration. Automatic plan/cache discovery,
the ordinary check workflow, larger dependency graphs and physical cache
retention/transfer costs still need work.

The integration driver's check stage accepts `-expected-actions` and
`-expected-hits`, asserting them before storing cache metadata. The actual
module passes the 102-action fanout fixture: cold 0/102 reuse, leaf edit
100/102, shared edit 0/102, and a breaking shared-library type change that
fails without publishing new cache metadata. This extends performance
measurement beyond the four-crate fixture; automatic cache discovery and
real registry/build-script coverage remain unfinished.

This branch records a native configuration-directory digest when capturing a
plan. Subsequent edits validate that digest in the engine instead of exporting
configuration files into the helper. Old plans keep their original validation
path. The original content/mode digest remains in compiler identities; the new
field does not change incremental-seed compatibility or compiler-result keys.
The integration writer also rejects manifest, lockfile, Cargo config, toolchain
config and permission changes.

Keep this as an experiment. The initial 102-crate comparison improved leaf
and shared medians, but a comparison using the same code, worker and initial
snapshots gives mixed results: leaf 0.956 to 0.988 s and shared 1.519 to 1.481 s.
Configuration exports disappear from profiles; a complete-command improvement
is not yet established. The measurements and limitations are in the Dagger
fork's `hack/rust-cache/NATIVE_CONFIGURATION.md`.
