#!/usr/bin/env bash
# Loopback web test on one 8-vCPU machine: `web serve` on CPUs 0-3, `web load` on CPUs 4-7.
# Run the same split on both arches; no network is involved, so the result compares the server CPU only.
# Read the output with docs/METHODOLOGY.md (web loopback): a step only counts when the server CPUs (cpu0-3 in
# <prefix>-cpustat.log) saturate first. /plaintext usually saturates the generator instead.
#
#   usage: web-loopback.sh <label-prefix>      (run from the folder that holds ./cpubench, ~10 minutes)
#   e.g.   web-loopback.sh arm64-pinned-loop
#   SERVER_CPUS / LOAD_CPUS pick the two CPU sets (default 0-3 and 4-7); use other sets on a machine whose
#   first CPUs also run system daemons (a k8s worker). MACHINE sets the `machine` field of the results
#   (default: the prefix); give a Docker run the machine of the native run so compare shows the overhead.
set -u
P=${1:?label prefix, e.g. x86-host-loop}
B=${CPUBENCH:-./cpubench}
SERVER_CPUS=${SERVER_CPUS:-0-3}
LOAD_CPUS=${LOAD_CPUS:-4-7}
D=results/web; mkdir -p "$D"
# per-CPU /proc/stat every 2s, to see which side saturates
( while :; do echo "T $(date +%s)"; grep -E '^cpu[0-9]+ ' /proc/stat; sleep 2; done ) > "$D/$P-cpustat.log" &
SAMP=$!
taskset -c "$SERVER_CPUS" "$B" web serve --addr 127.0.0.1:8080 > "$D/$P-serve.log" 2>&1 &
SRV=$!
trap 'kill $SRV $SAMP 2>/dev/null' EXIT
sleep 2
run() { # name endpoint step-duration
  echo "=== $1 start $(date +%T)"
  taskset -c "$LOAD_CPUS" "$B" web load --target http://127.0.0.1:8080 --endpoint "$2" --adaptive \
    --step-duration "$3" --warmup 5s --label "$P-$1" --machine "${MACHINE:-$P}" \
    -o "$D/$P-$1.json" --html "$D/$P-$1.html" 2>&1 | tee "$D/$P-$1.log"
  echo "=== $1 end $(date +%T)"
}
run plaintext /plaintext 10s
run mix mix 20s
echo ALLDONE
