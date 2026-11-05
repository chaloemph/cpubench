#!/usr/bin/env bash
# Phase 0: collect hardware/OS/environment info for cpubench (x86_64 + aarch64).
# Read-only. Run as root (or with sudo) to include DIMM details from dmidecode.
#   sudo bash scripts/collect-info.sh            -> <hostname>-<arch>-info.txt
#   sudo bash scripts/collect-info.sh out.txt

set -u
ARCH=$(uname -m)
OUT=${1:-"$(hostname -s)-${ARCH}-info.txt"}

section() { printf '\n== %s\n' "$1"; }
have() { command -v "$1" >/dev/null 2>&1; }
cat_if() { for f in "$@"; do [ -r "$f" ] && printf '%s: %s\n' "$f" "$(cat "$f" 2>/dev/null)"; done; }
as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"
  elif sudo -n true 2>/dev/null; then sudo "$@"
  else echo "(skipped: needs root, rerun with sudo)"; return 1
  fi
}

collect() {
  section "meta"
  echo "hostname: $(hostname)"
  echo "date: $(date -Is)"
  echo "arch: ${ARCH}"

  section "os / kernel"
  uname -a
  grep -E '^(PRETTY_NAME|VERSION_ID)=' /etc/os-release 2>/dev/null
  echo "pagesize: $(getconf PAGESIZE)"
  cat_if /sys/kernel/mm/transparent_hugepage/enabled

  section "virtualization / platform"
  have systemd-detect-virt && echo "virt: $(systemd-detect-virt 2>/dev/null)"
  cat_if /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name /sys/class/dmi/id/bios_version

  section "cpu (lscpu)"
  lscpu

  section "cpu (arch-specific)"
  case "$ARCH" in
    x86_64)
      grep -m1 'model name' /proc/cpuinfo
      echo "flags (relevant): $(grep -m1 '^flags' /proc/cpuinfo | grep -owE \
        'avx2|avx512f|avx512bw|avx512_vnni|amx_tile|aes|vaes|sha_ni|bmi2|adx|ht|hypervisor' | sort -u | tr '\n' ' ')"
      ;;
    aarch64)
      grep -m1 -E '^CPU implementer' /proc/cpuinfo
      grep -m1 -E '^CPU part' /proc/cpuinfo
      grep -m1 -E '^CPU variant' /proc/cpuinfo
      grep -m1 -E '^CPU revision' /proc/cpuinfo
      grep -m1 -E '^Features' /proc/cpuinfo
      ;;
  esac

  section "smt"
  cat_if /sys/devices/system/cpu/smt/control /sys/devices/system/cpu/smt/active
  cat_if /sys/devices/system/cpu/cpu0/topology/thread_siblings_list

  section "numa"
  if have numactl; then numactl -H; else
    echo "(numactl not installed)"
    for n in /sys/devices/system/node/node[0-9]*; do
      [ -d "$n" ] && echo "$(basename "$n"): cpus $(cat "$n/cpulist")"
    done
  fi

  section "frequency / power policy"
  cat_if /sys/devices/system/cpu/cpu0/cpufreq/scaling_driver \
         /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor \
         /sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_min_freq \
         /sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq \
         /sys/devices/system/cpu/cpufreq/boost \
         /sys/devices/system/cpu/intel_pstate/no_turbo
  [ -d /sys/devices/system/cpu/cpu0/cpufreq ] || echo "(no cpufreq: likely VM or firmware-controlled)"

  section "power sensors (local)"
  ls -d /sys/class/powercap/*rapl* 2>/dev/null || echo "rapl: not available"
  for h in /sys/class/hwmon/hwmon*; do [ -r "$h/name" ] && echo "$(basename "$h"): $(cat "$h/name")"; done
  have ipmitool && echo "ipmitool: installed" || echo "ipmitool: not installed"

  section "memory"
  free -g
  if have dmidecode; then
    as_root dmidecode -t memory | grep -E '^\s*(Locator|Bank Locator|Size|Speed|Configured Memory Speed|Part Number):' \
      | paste - - - - - - | grep -v 'No Module Installed'
  else
    echo "(dmidecode not installed)"
  fi

  section "network"
  for d in /sys/class/net/*; do
    i=$(basename "$d"); [ "$i" = lo ] && continue
    [ -e "$d/device" ] || continue
    rx=$(ls -d "$d"/queues/rx-* 2>/dev/null | wc -l)
    echo "$i: numa_node=$(cat "$d/device/numa_node" 2>/dev/null) speed=$(cat "$d/speed" 2>/dev/null)Mb/s rx_queues=$rx driver=$(basename "$(readlink -f "$d/device/driver")")"
  done
  if have systemctl; then echo "irqbalance: $(systemctl is-active irqbalance 2>/dev/null)"; fi

  section "container / cgroup"
  [ -f /.dockerenv ] && echo "inside docker: yes"
  [ -f /run/.containerenv ] && echo "inside podman: yes"
  [ -n "${KUBERNETES_SERVICE_HOST:-}" ] && echo "inside k8s: yes"
  echo "cgroup fs: $(stat -fc %T /sys/fs/cgroup 2>/dev/null)"
  cat_if /sys/fs/cgroup/cpu.max /sys/fs/cgroup/cpuset.cpus.effective /sys/fs/cgroup/memory.max

  section "tools / runtimes"
  for t in docker kubectl qemu-system-aarch64 qemu-system-x86_64 gcc go java node python3 php dotnet; do
    if have "$t"; then
      case "$t" in
        go) v=$(go version) ;;
        java) v=$(java -version 2>&1 | head -1) ;;
        kubectl) v=$(kubectl version --client 2>&1 | head -1) ;;
        *) v=$("$t" --version 2>&1 | head -1) ;;
      esac
      echo "$t: $v"
    fi
  done
  [ -f /etc/pve/.version ] && echo "proxmox: $(cat /etc/pve/.version)"
  have pveversion && pveversion
}

collect > "$OUT" 2>&1
echo "written: $OUT"
