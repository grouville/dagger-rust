#!/usr/bin/env python3
"""Alternating A/B timing of two commands. Usage:
  pairs.py --pairs N --cwd DIR [--env K=V ...] -- "A cmd" "B cmd"
Prints per-arm median, p95, max, count >1s, and median paired saving (A-B)."""
import argparse, os, shlex, statistics, subprocess, sys, time

def run(cmd, cwd, env, i=0):
    cmd = cmd.replace("{i}", str(i))
    t = time.perf_counter()
    p = subprocess.run(shlex.split(cmd), cwd=cwd, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    ms = (time.perf_counter() - t) * 1000
    if p.returncode != 0:
        sys.stderr.write(f"FAIL ({p.returncode}) {cmd}\n{p.stderr.decode()[-500:]}\n")
    return ms, p.returncode

def p95(xs): xs = sorted(xs); return xs[min(len(xs)-1, int(round(0.95*(len(xs)-1))))]

ap = argparse.ArgumentParser()
ap.add_argument("--pairs", type=int, default=20)
ap.add_argument("--cwd", default=".")
ap.add_argument("--env", action="append", default=[])
ap.add_argument("--csv")
ap.add_argument("--offset", type=int, default=0)
ap.add_argument("cmds", nargs=2)
a = ap.parse_args()
env = dict(os.environ)
for kv in a.env:
    k, v = kv.split("=", 1); env[k] = v
A, B = a.cmds
# warmup pair, discarded
run(A, a.cwd, env, a.offset + 999); run(B, a.cwd, env, a.offset + 999)
ra, rb, fails = [], [], 0
rows = []
for i in range(a.pairs):
    order = [("A", A), ("B", B)] if i % 2 == 0 else [("B", B), ("A", A)]
    got = {}
    for label, cmd in order:
        ms, rc = run(cmd, a.cwd, env, a.offset + i); got[label] = ms; fails += rc != 0
        rows.append((i, label, f"{ms:.1f}", rc))
    ra.append(got["A"]); rb.append(got["B"])
    print(f"pair {i:2d} {'AB' if i%2==0 else 'BA'}  A={got['A']:8.1f}  B={got['B']:8.1f}  A-B={got['A']-got['B']:+8.1f}", flush=True)
d = [x - y for x, y in zip(ra, rb)]
print(f"\nA: median {statistics.median(ra):.1f}  p95 {p95(ra):.1f}  max {max(ra):.1f}  >1s {sum(x>1000 for x in ra)}/{len(ra)}")
print(f"B: median {statistics.median(rb):.1f}  p95 {p95(rb):.1f}  max {max(rb):.1f}  >1s {sum(x>1000 for x in rb)}/{len(rb)}")
print(f"paired saving A-B: median {statistics.median(d):+.1f}  B faster in {sum(x>0 for x in d)}/{len(d)} pairs  failures={fails}")
if a.csv:
    with open(a.csv, "w") as f:
        f.write("pair,arm,ms,rc\n"); [f.write(",".join(map(str, r)) + "\n") for r in rows]
