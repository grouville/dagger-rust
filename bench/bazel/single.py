#!/usr/bin/env python3
"""Time N samples of one command ({i} = sample index). usage: single.py --n N [--offset K] [--label L] -- cmd"""
import argparse, shlex, statistics, subprocess, sys, time
ap = argparse.ArgumentParser(); ap.add_argument("--n", type=int, default=6); ap.add_argument("--offset", type=int, default=0); ap.add_argument("--label", default=""); ap.add_argument("cmd")
a = ap.parse_args()
xs = []; fails = 0
subprocess.run(shlex.split(a.cmd.replace("{i}", str(a.offset + 999))), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)  # warmup
for i in range(a.n):
    t = time.perf_counter(); p = subprocess.run(shlex.split(a.cmd.replace("{i}", str(a.offset + i))), stdout=subprocess.DEVNULL, stderr=subprocess.PIPE); ms = (time.perf_counter() - t) * 1000
    fails += p.returncode != 0; xs.append(ms)
    if p.returncode != 0: sys.stderr.write(p.stderr.decode()[-300:] + "\n")
xs_s = sorted(xs); p95 = xs_s[min(len(xs_s)-1, int(round(0.95*(len(xs_s)-1))))]
print(f"{a.label:28} n={a.n} median {statistics.median(xs):8.1f}  p95 {p95:8.1f}  max {max(xs):8.1f}  fails={fails}")
