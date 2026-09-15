#!/usr/bin/env bash
# usage: bflow.sh <daemon|coldclient|fresh> <exact|app|lib|test> <n>
#  daemon     : resident bazel server, warm output base (Bazel's best case)
#  coldclient : `bazel shutdown` before the command, so server start is included (no resident process, like Dagger)
#  fresh      : brand-new --output_base per sample with the warm --disk_cache (remote-cache stand-in)
set -eu
S=$BENCH_ROOT
mode=$1; flow=$2; n=$3
root=$S/ripgrep-bazel
cd $root
case $flow in
  app) python3 $S/edit.py $root app $n ;;
  lib) python3 $S/edit.py $root lib $n ;;
esac
BZ=$S/bin/bazelisk
targets="//:rg //crates/..."
[ $flow = test ] && cmd=test || cmd=build
case $mode in
  daemon)     exec $BZ $cmd $targets ;;
  coldclient) $BZ shutdown >/dev/null 2>&1; exec $BZ $cmd $targets ;;
  fresh)      ob=$S/bazel-ob-$n; rm -rf $ob; exec $BZ --output_base=$ob $cmd $targets ;;
esac
