#!/usr/bin/env bash
# usage: mflow.sh <project> <native|dagger> <exact|leaf|core|checkall> <n>
set -eu
S=$BENCH_ROOT
proj=$1; side=$2; flow=$3; n=$4
export HOME=$S/home-nocloud XDG_CONFIG_HOME=$S/home-nocloud/.config DO_NOT_TRACK=1
export DAGGER_ENGINE=${DAGGER_ENGINE:-container://dagger-engine.main}
DAGGER=${DAGGER:-$S/wt-main/bin/dagger}
leaf=$(python3 -c "import tomllib;print(tomllib.load(open('$S/projects.toml','rb'))['$proj']['leaf'])")
core=$(python3 -c "import tomllib;print(tomllib.load(open('$S/projects.toml','rb'))['$proj']['core'])")
flags=$(python3 -c "import tomllib;print(tomllib.load(open('$S/projects.toml','rb'))['$proj'].get('flags',''))")
ctr=rust-native-$proj
case $side in
  native)
    root=$S/projects/$proj-native
    case $flow in
      leaf) python3 $S/edit.py $root file $leaf $n; docker cp $root/$leaf $ctr:/src/$leaf ;;
      core) python3 $S/edit.py $root file $core $n; docker cp $root/$core $ctr:/src/$core ;;
      checkall) python3 $S/edit.py $root file $leaf $n; docker cp $root/$leaf $ctr:/src/$leaf ;;
    esac
    if [ $flow = checkall ]; then
      exec docker exec $ctr sh -c "cargo check --workspace --locked $flags && cargo clippy --workspace --locked $flags && cargo test --workspace --locked $flags && cargo fmt --all -- --check"
    else
      exec docker exec $ctr cargo check --workspace --locked $flags
    fi ;;
  dagger)
    root=$S/projects/$proj
    cd $root
    case $flow in
      leaf) python3 $S/edit.py $root file $leaf $n; exec $DAGGER check rust:check ;;
      core) python3 $S/edit.py $root file $core $n; exec $DAGGER check rust:check ;;
      exact) exec $DAGGER check rust:check ;;
      checkall) python3 $S/edit.py $root file $leaf $n; exec $DAGGER check --no-generate ;;
    esac ;;
esac
