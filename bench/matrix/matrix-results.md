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
