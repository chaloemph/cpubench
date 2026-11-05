#!/usr/bin/env bash
# Runs the archprobe checks for memory placement problems on VMs and NUMA hosts. ~6 minutes. Usage: run-all.sh ./archprobe
# Read the output as: is private-atomic speed and DRAM latency the same on every page? If a share of the
# pages is ~9x slower for atomics and ~2x slower for loads, memory is not local to the CPU (NUMA/VM placement).
B=${1:-./archprobe}
echo "== 1. private-atomic speed per 4 KB page, 3 executions (same pages must give the same bin twice)"
for i in 1 2 3; do "$B" -mode pages; done
echo
echo "== 2. false-sharing check: total Mops/s of per-thread atomics by stride (cols: 1 2 4 8 threads)"
for s in 64 128 256; do
  printf "stride %-5s" $s
  for t in 1 2 4 8; do printf "%9s" "$("$B" -mode atomic -stride $s -threads $t | awk '/RESULT/{print $2}')"; done; echo
done
echo
echo "== 3. DRAM latency on fast-atomic pages vs slow-atomic pages (about 2.5 minutes)"
"$B" -mode classlat -mb 512
echo
echo "== 4. memory bandwidth (triad GB/s, best of 3) and latency (ns, best of 3), THP off/on"
best() { local sel=$1; shift; for i in 1 2 3; do "$B" "$@" | awk '/RESULT/{print $2, $3}'; done | sort -g | { [ "$sel" = max ] && tail -1 || head -1; }; }
printf "%-10s%16s%16s%16s%16s\n" "triad" "1 thr" "2 thr" "4 thr" "8 thr"
for t in "" "-thp"; do printf "thp=%-5s" "${t:+on}"; [ -z "$t" ] && printf "\b\b\b\boff  "
  for n in 1 2 4 8; do read v h <<<"$(best max -mode triad -threads $n $t)"; printf "%16s" "$v ($h kB THP)"; done; echo; done
printf "%-10s%16s%16s%16s%16s\n" "latency" "8 MB" "32 MB" "128 MB" "512 MB"
for t in "" "-thp"; do printf "thp=%-5s" "${t:+on}"; [ -z "$t" ] && printf "\b\b\b\boff  "
  for m in 8 32 128 512; do read v h <<<"$(best min -mode lat -mb $m $t)"; printf "%16s" "$v ($h kB THP)"; done; echo; done
