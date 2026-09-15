# Rust

Cargo-compatible checks and build artifacts for an existing Cargo workspace,
through ordinary Dagger commands.

## Usage

In the Cargo workspace root, `dagger.toml`:

```toml
[modules.rust]
source = "github.com/grouville/dagger-rust"

[modules.rust.settings]
locked = true
cacheKey = "my-project-rust"
```

Then:

```sh
dagger check rust:check                 # cargo check
dagger check rust:fmt rust:clippy rust:test
dagger check                            # all of the above + generated-artifact drift
dagger generate -y                      # cargo build, publish artifacts to target/dagger
```

Checks take the project source as a contextual input, so an unchanged tree is a
cache hit across commands. Cargo's source reconciliation and its intermediate
build directory live in locked cache volumes; final artifacts are immutable
Dagger results. The module owns `target/dagger` only. With `locked = false`,
`dagger generate` also publishes Cargo's actual `Cargo.lock`.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `image` | pinned `rust` Debian bookworm image | toolchain image (only the pinned digest is supported today) |
| `cacheKey` | `rust-v3` | prefix of the source/registry/git/build cache volumes; use one per project |
| `locked` | `false` | pass `--locked` to Cargo and require an existing `Cargo.lock` |
| `cargoProfile`, `target`, `features`, `allFeatures`, `noDefaultFeatures` | unset | forwarded to Cargo for check and build alike |
| `pinnedSourceSync` | `true` | install rsync from checksum-pinned Debian packages instead of `apt-get` |
| `prepareProjectToolchain` | `true` | honor a root `rust-toolchain(.toml)` in a cached toolchain layer |

## Limitations

- Invoke from the workspace root; nested-directory invocation is not supported.
- Linux/amd64 Debian image only; artifacts are Linux/GNU outputs.
- Requires stable Cargo >= 1.91 (`build.build-dir`); it does not upgrade the toolchain.
- `.git`, `target`, `dagger.toml` and `dagger.lock` are excluded from the Cargo source.
- Symlink artifacts and workspaces with no final artifacts are rejected by the generator.
