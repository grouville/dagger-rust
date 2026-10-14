# Whole native compiler-input snapshots

This branch tests copying the selected latest compiler batch into one fresh
input snapshot before each eligible build. That avoids filtered seed assembly
and periodic compaction. The worker removes outputs being rebuilt before
scheduling compilers. Mixed seed generations and reversions use the existing
path. Compiler compatibility keys remain unchanged, mounted seeds remain
ordinary inputs, and no compiler-state CacheVolume is used.

The experiment is rejected as a performance optimization:

| Median edited normal CLI command | Original copying | Whole input | Four-build branching |
| --- | ---: | ---: | ---: |
| Shared library, twelve rounds | 1.390 s | 1.413 s | 1.341 s |
| Leaf library, eight rounds | 0.859 s | 0.930 s | 0.898 s |

Native Cargo takes about 0.48 s for shared edits and 0.09 s for leaf edits.
Whole-input copying beats the original in 4/12 shared rounds and 0/8 leaf
rounds. Variants have separate sources, targets and project keys; order rotates
on the same engine. Timing includes startup and shutdown. Telemetry remains
enabled locally, and wcprof is disabled during timing.

Across six storage stages after 25 shared edits, filesystem usage is stable at
57.7 MB versus 57.4 MB for original copying, counting hard links once. Whole
input retains twelve physical snapshots with a longest chain of four, versus
twenty snapshots and a chain of five. Those storage results do not justify the
command slowdown.

The real module reuses an older depth-eight cache's exact results, then produces
depth-one batches. Type failures, retained reversion and a new edit afterward
pass. Focused compiler race tests pass. Six clean engine restarts recover all
three project caches without host state. Native transfer checks are separate
from timing.

Keep this branch isolated. The broader task still needs native build/test
entrypoints, real Cargo dependency support, remote discovery and faster complete
commands. Full measurements are in `hack/rust-cache/NATIVE_WHOLE_INPUT.md` on
`grouville/dagger:experiment/rust-native-cache`.
