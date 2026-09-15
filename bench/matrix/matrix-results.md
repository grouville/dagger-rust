# Project matrix (medians, ms; native = cargo via docker exec in the same image; Dagger = module, per-action volumes, engine pinned, logged out)

| Project | flow | native | Dagger | overhead |
|---|---|---:|---:|---:|
| ripgrep | exact | 110 | 556 | 446 |
| ripgrep | app edit | 317 | 1230 | 913 |
| ripgrep | lib edit | 483 | 1347 | 864 |
| ripgrep | edit + 4 checks | 7107 | 6493 | -614 |
| fd (--no-default-features) | exact | 211 | 578 | 367 |
| fd | leaf edit | 374 | 1227 | 853 |
| fd | core edit | 374 | 1266 | 892 |
| axum | exact | 292 | 683 | 391 |
| axum | leaf edit (axum-extra) | 391 | 1468 | 1077 (one 6.7s Dagger sample) |
| axum | core edit (axum-core) | 1106 | 2015 | 909 |
| helix | (not measurable) | - | - | module requires Cargo >= 1.91 (`build.build-dir`); helix pins rust-toolchain 1.90.0 and the guard refuses. Native `cargo check` also fails in this image (helix-term build script). |
| ruff (toolchain 1.98.0, make added to the module) | exact | 532 | 977 | 445 |
| ruff | leaf edit (crates/ruff) | 1202 | 3238 | 2036 |
| ruff | core edit (ruff_python_ast) | 9837 | 12507 | 2427 (n=3, native had a 32.7s sample) |
