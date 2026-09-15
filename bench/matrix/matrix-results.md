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
| helix (Cargo 1.90 via the old-Cargo layout; extraPackages git, g++) | exact | 1890 | 626 | -1264 (native reruns its build script; the module caches) |
| ruff (toolchain 1.98.0, make added to the module) | exact | 532 | 977 | 445 |
| ruff | leaf edit (crates/ruff) | 1202 | 3238 | 2036 |
| ruff | core edit (ruff_python_ast) | 9837 | 12507 | 2427 (n=3, native had a 32.7s sample) |
| helix | leaf edit (helix-term) | 2084 | 3872 | 1788 |
| helix | core edit (helix-core) | 2218 | 3329 | 1111 |

Notes: helix needed the old-Cargo layout (pins 1.90.0), `extraPackages = ["git", "g++"]`, and the gitignore-aware mirror
(without it every edit refetched 301 tree-sitter grammars: ~50 min). Its build script reruns on every native check too.
