#!/usr/bin/env bash
# Create a benchmark VM on a Proxmox node with settings that keep x86 and ARM
# comparable. Prints the qm commands only, unless APPLY=1.
#
#   VMID=9100 VM_NAME=bench-arm CORES=16 MEM=32768 NUMA_NODE_CPUS=0-15 NUMA_HOST_NODE=0 \
#   IMAGE=/var/lib/vz/template/iso/noble-server-cloudimg-arm64.img \
#   SSH_KEY=~/.ssh/id_ed25519.pub bash create-bench-vm.sh            # review
#   ... APPLY=1 bash create-bench-vm.sh                                # create
#
# Why these settings:
#   cpu=host        x86 default types (kvm64, x86-64-v2-AES) hide AVX2/AVX-512 and skew crypto/compress
#   numa=1 + affinity + numa0 policy=bind
#                   keep vCPUs AND guest memory on one host NUMA node (multi-socket servers and many arm64 CPUs have several).
#                   affinity only pins the vCPU threads; without the numa0 bind line a share of the guest's
#                   pages can land on another node, where atomics are ~9x and DRAM loads ~2x slower (measured on an
#                   arm64 VM, see docs/METHODOLOGY.md). Needs enough free memory on that node.
#   balloon=0       ballooning changes memory under load
#   virtio multiqueue  one queue per vCPU so the guest NIC is not the bottleneck in web tests
#   arm64           needs UEFI (OVMF/AAVMF) and an EFI disk; there is no SeaBIOS on ARM
set -euo pipefail

VMID=${VMID:?set VMID}
VM_NAME=${VM_NAME:-cpubench-$VMID}
CORES=${CORES:-8}
MEM=${MEM:-16384}
STORAGE=${STORAGE:-local-lvm}
BRIDGE=${BRIDGE:-vmbr0}
IMAGE=${IMAGE:?set IMAGE to an Ubuntu 22.04/24.04 cloud image for this arch}
NUMA_NODE_CPUS=${NUMA_NODE_CPUS:-}   # host CPUs of one NUMA node, e.g. 0-31 (see: numactl -H)
NUMA_HOST_NODE=${NUMA_HOST_NODE:-}   # that node's id, e.g. 0: guest memory is bound to it
SSH_KEY=${SSH_KEY:-}
SNIPPET=${SNIPPET:-}                 # e.g. local:snippets/cpubench-user-data.yaml
ARCH=${ARCH:-$(uname -m)}

run() {
  if [ "${APPLY:-0}" = 1 ]; then echo "+ $*"; "$@"; else echo "$*"; fi
}

run qm create "$VMID" --name "$VM_NAME" --ostype l26 \
  --cores "$CORES" --sockets 1 --cpu host --numa 1 \
  --memory "$MEM" --balloon 0 \
  --scsihw virtio-scsi-single \
  --net0 "virtio,bridge=$BRIDGE,queues=$CORES" \
  --serial0 socket --vga serial0 --agent enabled=1

if [ "$ARCH" = aarch64 ]; then
  run qm set "$VMID" --bios ovmf --efidisk0 "$STORAGE:1,efitype=4m,pre-enrolled-keys=0"
fi

run qm set "$VMID" --scsi0 "$STORAGE:0,import-from=$IMAGE,iothread=1,discard=on"
run qm resize "$VMID" scsi0 40G
run qm set "$VMID" --boot order=scsi0 --ide2 "$STORAGE:cloudinit" --ipconfig0 ip=dhcp --ciuser ubuntu
[ -n "$SSH_KEY" ] && run qm set "$VMID" --sshkeys "$SSH_KEY"
[ -n "$SNIPPET" ] && run qm set "$VMID" --cicustom "user=$SNIPPET"
[ -n "$NUMA_NODE_CPUS" ] && run qm set "$VMID" --affinity "$NUMA_NODE_CPUS"
[ -n "$NUMA_HOST_NODE" ] && run qm set "$VMID" --numa0 "cpus=0-$((CORES - 1)),hostnodes=$NUMA_HOST_NODE,memory=$MEM,policy=bind"
if [ -n "$NUMA_NODE_CPUS" ] && [ -z "$NUMA_HOST_NODE" ]; then
  echo "# WARNING: NUMA_NODE_CPUS pins only the vCPUs; set NUMA_HOST_NODE=<node id> to bind guest memory too" >&2
fi

echo
echo "# Next: qm start $VMID, then inside the guest: cpubench doctor && cpubench run --label $VM_NAME --machine <host> --upload http://<hub>:8080"
[ "${APPLY:-0}" = 1 ] || echo "# (dry run: set APPLY=1 to execute)"
