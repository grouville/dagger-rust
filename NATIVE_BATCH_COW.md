# Native compiler snapshot branching

This experiment mounts the latest native compiler batch as the next worker's
writable input when it supplies every selected seed and needs no artifact
overlays. The engine branches the immutable snapshot. The worker removes the
outputs being rebuilt before scheduling compilers, preserving exact hits and
rustc incremental state. Other seed selections use the existing copying path.

After eight consecutive builds, copy into a fresh directory to break physical
ancestry. Native checkpoints close the producer recipe; their snapshots still
have filesystem parents. Seeds remain ordinary cache-key inputs. No CacheVolume
is used for compiler state.

Ten alternating shared-edit pairs through normal `dagger check rust:check`
measure 1.405 s before and 1.344 s after, with nine pairs faster. Cargo takes
0.476/0.483 s. Six leaf pairs do not establish a gain. Compaction samples remain
included, and the shared compaction run is slower than its control. Telemetry is
enabled through a local collector; wcprof is disabled during timing. Host
contention limits the performance claim.

Diagnostic profiles reduce critical-path directory copies from 150.95 ms to
9.79 ms. The candidate's profiled command is nevertheless slower under higher
CPU pressure. This proves copy removal, not another command speedup.

The actual module passes eighteen shared edits, two compactions, failed type
changes, recent-version reversion and another edit. Native cache transfer and a
new edited build pass on a second engine. Automatic discovery after a clean
restart reuses 4/4 actions, then 1/4 after a shared edit. Focused compiler race
tests pass.

Twenty-five edits retain 55–56 snapshot references after pruning, and hint
eviction followed by pruning releases every Rust cache object. An offline walk
of physical parents after 17 edits accounts for 114.5 MB versus 85.5 MB in the
copying path. The branch retains fewer snapshot IDs but 34% more accounted data.
These totals are snapshot metadata accounting, rather than unique allocated
extents or remote transfer bytes.

Keep this branch separate. Compare a shorter compaction interval before adopting
it; edited-command tail latency, peak storage and transfer costs matter alongside
the median. Registry dependencies, build scripts, procedural macros and test
support remain unfinished. The main report is `hack/rust-cache/NATIVE_BATCH_COW.md`
on `grouville/dagger:experiment/rust-native-cache`.
