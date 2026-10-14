# Experimental native compiler cache

The `compiler` dependency contains the tested Rust replay backend, extracted
from the Dagger engine experiment. It shares the module's existing API
connection. Compiler outputs and rustc incremental state are immutable native
directories; the cache consists of a manifest, batch tokens and Directory
fields. No Cargo compiler cache volume is used in this path.

`captureCheckPlan` records a locked, path-only workspace's Cargo check graph.
Pass that File as `compilerPlan` and retain `nativeCheck.cache` as
`compilerCache`. `check` uses this backend when a plan is configured. The normal
Cargo backend remains the default while automatic plan/cache discovery and
support for registry dependencies, build scripts, procedural macros and tests
are implemented. Published artifacts retain their existing reproducibility
policy.

`restoreCompilerCache` reconstructs transferred metadata with native Directory
objects on the receiver. Local numeric object IDs are not a portable cache.
The module uses its own public cache/result types rather than exposing types
owned by its implementation dependency.

The model, replay, worker and compiler-wrapper tests pass after extraction.
The complete generated Go module builds, and the actual Rust entrypoint reaches
native compilation. The module-level reuse/transfer test is still in progress;
these statements do not establish complete integration or a performance win.

That integration found an engine hash inconsistency: editing one file in a host
import with `Directory.withNewFile` changes digests of untouched sibling
directories. A focused test on the active import path fails and a diagnostic
confirms all three unchanged Rust libraries change hashes. The fix must retain
correct mutation tracking without changing the filesync hash formatter. This
needs resolution before the Directory-edit integration can pass.

Host imports are snapshots within one client session. Testing actual host edits
must use fresh command sessions; importing the same path again in one session
returns the original snapshot. The current integration driver is diagnostic
work in progress and still needs that command boundary.

The module generator currently forces SDK beta.12. The compiler module retains
beta.17 through a version replacement because replay requires the separate
`core` bindings. The local integration engine's CLI, session helpers and builtin
generator also had to be built from the engine's source. Its old embedded CLI
advertised beta.16 and failed nested calls against the beta.17 engine.

No upstream PR is open for this integration. Complete-command edited-build
measurements, transferred cache reuse and automatic selection remain required.
