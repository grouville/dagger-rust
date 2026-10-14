# Native compiler snapshot branching

This branch compacts compiler state after four consecutive builds. It accepts
eight-build histories, preserves their exact result hits, and compacts their
selected state on the next miss. Compaction uses `depth >= interval`, so loading
an older depth-eight batch cannot skip it. The interval is a storage policy;
compiler compatibility keys remain unchanged.

The worker mounts the latest native batch when it supplies every selected seed
and needs no artifact overlays. The engine branches that immutable snapshot.
Before scheduling compilers, remove outputs being rebuilt. Exact-hit artifacts
and rustc incremental state remain available. Mixed seed generations and
reversions use the existing copying path. Seeds are normal execution inputs,
and no compiler-state CacheVolume is used.

A three-way comparison of normal `dagger check rust:check` commands gives:

| Median edited command | Copying | Eight builds | Four builds |
| --- | ---: | ---: | ---: |
| Shared library, 16 rounds | 1.413 s | 1.366 s | 1.350 s |
| Leaf library, eight rounds | 0.855 s | 0.851 s | 0.862 s |

Native Cargo takes about 0.48 s for shared edits and 0.09 s for leaf edits.
Four builds beat copying and eight builds in 13/16 shared rounds. Leaf edits
do not establish a gain. Compaction runs and a slow copying sample remain in
the results. Each variant has its own source, Cargo target and project key;
order rotates on the same engine. Telemetry stays enabled locally, and wcprof
is disabled for timing.

The storage comparison makes 25 shared edits per policy and measures physical
parent chains at six stages. Filesystem usage, counting hard links once, peaks
at 57.4 MB for copying, 124.0 MB for eight builds and 85.9 MB for four builds.
Four builds therefore still retain about 50% more than copying at the sampled
peak. Whole-cache usage and snapshot accounting are different measurements.

The real module passes depth-eight migration with 4/4 exact hits, immediate
compaction on an edit, repeated four-build cycles, failed type changes, retained
reversion and another edit. Focused compiler race tests pass. Each of six clean
engine restarts rediscovers all three project caches, with no host cache state.

Keep the experiment separate. Test assembling one fresh input snapshot per
build next: simpler copying might avoid filtering work and long physical chains
together. The complete-command target, remote discovery and full Cargo feature
coverage remain unfinished. Full measurements are in
`hack/rust-cache/NATIVE_COW_INTERVALS.md` on
`grouville/dagger:experiment/rust-native-cache`.
