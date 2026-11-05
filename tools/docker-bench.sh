#!/usr/bin/env bash
# Docker run of the same tests as the native VM runs: `cpubench auto --profile standard`, then the loopback
# web test (tools/web-loopback.sh) inside the same container. No image build: a stock ubuntu:24.04 plus the
# cpubench binary mounted from the host, so both arches run the very same binary as the native runs.
#
#   usage: docker-bench.sh <label-prefix> <machine>      (run from the folder that holds ./cpubench and web-loopback.sh)
#   e.g.   docker-bench.sh x86-host-docker x86-host
#
# <machine> is the label of the native run on the same VM, so `cpubench compare` shows Docker vs native.
# CPUS / MEMORY must be the same on both arches (default: 8 CPUs, 12 GiB; the VMs have 8 vCPU).
set -eu
P=${1:?label prefix, e.g. x86-host-docker}
M=${2:?machine, the native label on this VM}
CPUS=${CPUS:-8}
MEMORY=${MEMORY:-12g}
IMAGE=${IMAGE:-ubuntu:24.04}
OUT=${OUT:-$PWD/docker-out}
# machine of the native loopback run on this VM (arm64-pinned-1 -> arm64-pinned-loop, x86-host -> x86-host-loop)
WEB_MACHINE=${WEB_MACHINE:-$(echo "$M" | sed -E 's/-[0-9]+$//')-loop}
mkdir -p "$OUT"
docker run --rm --name "$P" --cpus "$CPUS" --memory "$MEMORY" --user root \
  -v "$PWD/cpubench:/usr/local/bin/cpubench:ro" -v "$PWD/web-loopback.sh:/usr/local/bin/web-loopback.sh:ro" \
  -v "$OUT:/out" -w /out "$IMAGE" bash -c '
    set -u
    cpubench info > /out/'"$P"'-info.txt 2>&1
    cpubench auto --profile standard --label '"$P"' --machine '"$M"' \
      -o /out/'"$P"'.json --html /out/'"$P"'.html 2>&1 | tee /out/'"$P"'-auto.log
    echo "=== loopback web"
    CPUBENCH=cpubench MACHINE='"$WEB_MACHINE"' bash /usr/local/bin/web-loopback.sh '"$P"'-loop
  '
echo DOCKERDONE
