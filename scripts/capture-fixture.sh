#!/usr/bin/env bash
# Copy the /proc and /sys files cpubench's probe reads into a directory tree,
# so the parser can be tested against this machine on any other machine.
#   sudo bash scripts/capture-fixture.sh testdata/fixtures/<name>
# Read-only on the source machine. Run with sudo to include DIMM details.
set -u
OUT=${1:?usage: capture-fixture.sh <out-dir>}
mkdir -p "$OUT"

copy() {
  for f in "$@"; do
    [ -r "$f" ] || continue
    mkdir -p "$OUT$(dirname "$f")"
    cat "$f" > "$OUT$f" 2>/dev/null || true
  done
}

copy /proc/cpuinfo /proc/meminfo /proc/sys/kernel/osrelease /proc/self/cgroup /etc/os-release /etc/hostname
copy /sys/devices/system/cpu/online /sys/devices/system/cpu/smt/active \
     /sys/devices/system/cpu/cpufreq/boost /sys/devices/system/cpu/intel_pstate/no_turbo
copy /sys/devices/system/cpu/cpu*/topology/physical_package_id /sys/devices/system/cpu/cpu*/topology/core_id
copy /sys/devices/system/cpu/cpu0/cache/index*/{level,type,size,shared_cpu_list}
copy /sys/devices/system/cpu/cpu0/cpufreq/{cpuinfo_max_freq,scaling_governor}
copy /sys/devices/system/cpu/vulnerabilities/*
copy /sys/devices/system/node/node*/{cpulist,distance,meminfo}
copy /sys/class/dmi/id/{sys_vendor,product_name,bios_version,bios_vendor,bios_date}
copy /sys/kernel/mm/transparent_hugepage/enabled
copy /sys/fs/cgroup/cgroup.controllers
cg=$(sed -n 's/^0:://p' /proc/self/cgroup)
copy /sys/fs/cgroup${cg%/}/{cpu.max,cpuset.cpus.effective,memory.max,cpu.stat}

for d in /sys/class/net/*; do
  [ -e "$d/device" ] || continue
  copy "$d/speed" "$d/device/numa_node"
  for q in "$d"/queues/rx-*; do [ -e "$q" ] && mkdir -p "$OUT$q"; done
done

# storage and network controllers (PCI class 01xxxx / 02xxxx) and their driver
for d in /sys/bus/pci/devices/*; do
  case "$(cat "$d/class" 2>/dev/null)" in 0x01*|0x02*) ;; *) continue ;; esac
  copy "$d"/{class,vendor,device,numa_node,current_link_speed,current_link_width,max_link_speed,max_link_width}
  [ -L "$d/driver" ] && ln -sfn "$(readlink "$d/driver")" "$OUT$d/driver"
done
for d in /sys/block/*; do
  [ -e "$d/device" ] || continue
  copy "$d/size" "$d/queue/rotational" "$d/device/model"
done

getconf PAGESIZE > "$OUT/pagesize"
if [ "$(id -u)" -eq 0 ] && command -v dmidecode >/dev/null; then
  dmidecode -t 17 > "$OUT/dmidecode-17.txt"
fi
echo "captured into $OUT ($(find "$OUT" -type f | wc -l) files)"
