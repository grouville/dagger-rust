# ripgrep under Bazel (rules_rust 0.74.0), same flows as the Dagger module

Experiment, not part of the module. Overlay for the pinned ripgrep checkout
(`3fce3b5bb0236da2df6d99672afb8a719642eca7`): `overlay/` holds `MODULE.bazel`
(rules_rust 0.74.0, rustc 1.97.1 like the module's image, `crate_universe` from
the workspace `Cargo.lock`), `.bazelrc` (`pipelined_compilation`, a `--disk_cache`)
and one `BUILD.bazel` per workspace crate generated from `cargo metadata`
(default Cargo features, workspace deps as labels, `include_str!` data as
`compile_data`; `grep-pcre2` and `grep-index` are optional features and are not
built). `bflow.sh` runs one flow in one of three modes, `single.py` times it.

Setup: copy `overlay/` over a ripgrep checkout, `bazelisk build //:rg //crates/...`
with `CARGO_BAZEL_REPIN=1` once. Cold full build here: 111s, 239 sandboxed actions.

Results on one Linux/amd64 box (medians, ms; native = `cargo check --workspace
--locked` in the same image; Dagger = `dagger check rust:check`, engine pinned,
logged out):

| Flow | native | Dagger | Bazel daemon | Bazel cold client | Bazel fresh machine |
|---|---:|---:|---:|---:|---:|
| exact unchanged | 110 | 556 | 538 (n=8) | 8382 (n=6) | 8222 (n=3) |
| app edit (`crates/core/flags/doc/version.rs`) | 317 | 1230 | 2449 (n=6) | 10017 (n=5) | |
| library edit (`crates/printer/src/standard.rs`) | 483 | 1347 | 2669 (n=6) | | |

Is that comparable? The no-op rows compare like with like. On edits, `bazel build`
compiles crates fully while the Dagger and native rows run `cargo check`, which stops at
metadata. Two ways to make it fair:

Check vs check: rules_rust exposes the `.rmeta` artifact through the `build_metadata` output
group (with `pipelined_compilation`), so `bazel build --output_groups=build_metadata
//crates/...` is the check-equivalent (the `rg` binary has no metadata output and is excluded):

| Flow (metadata only) | native `cargo check` | Dagger `check rust:check` | Bazel daemon `build_metadata` |
|---|---:|---:|---:|
| library edit | 483 | 1347 | 980 (n=6; 3 sandboxed actions, ~540 of it is the client/server round trip) |

Build vs build:

| Flow (full `cargo build` / `bazel build`) | native | Dagger (`call rust compile`) | Bazel daemon |
|---|---:|---:|---:|
| app edit | 644 (n=5) | 1803 (n=5, one 7.0s sample) | 2449 (n=6) |

(The `rustc_rmeta_output` group is a diagnostics file group, empty unless
`rustc_output_diagnostics` is on; requesting it builds nothing, which is expected.)

- daemon: resident Bazel server, warm output base (Bazel's best case).
- cold client: `bazel shutdown` before each sample, i.e. no resident process, which is
  Dagger's situation on every command.
- fresh machine: new `--output_base` per sample with the warm `--disk_cache`, the
  stand-in for a remote cache hit on a machine that has never built the project.

Reading: with a resident server Bazel ties Dagger on a no-op and is ~2x slower on an
edit (sandboxed actions cannot use rustc's incremental state; Dagger keeps it in a
per-action cache volume). Without a resident server, or on a fresh machine, every
Bazel command pays ~8s of server start and analysis before any cache hit. What Bazel
has and Dagger does not is that its per-crate outputs are shareable across machines;
see the module plan's dependency-layer note.
