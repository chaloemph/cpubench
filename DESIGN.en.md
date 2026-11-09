# cpubench design

[ภาษาไทย](DESIGN.md) · English

This document is written so that people **and agents** who develop cpubench further can start work without reading earlier conversations.

## Goal

Measure and compare x86-64 (amd64) and ARM64 servers neutrally, with no tie to a vendor, CPU model or migration direction. It answers both "can we move across architectures?" and "which vendor is better value within one architecture?" for web APIs / web apps on bare metal, VM, Docker or Kubernetes, using this data:

1. **Performance**: micro (per core / per machine), web (Max RPS @ SLO, p99 at low load)
2. **Value**: RPS/W, power curve, number of servers needed, 5-year TCO
3. **Stability**: stress, soak, spike, checksum, MCE
4. **Readiness**: overhead of each layer (native/VM/Docker/K8s), density, startup/warm-up, and which images/binaries have a build for the target architecture

## Architecture: hexagonal-lite

```
cmd/cpubench            composition root: wires adapter → app → cli
internal/
  domain/               ❶ pure logic, no I/O
    hardware/           System, Topology, PlacementOrder, MIDR lookup
    benchmark/          Workload interface, Result, Stats
    doctor/             environment rules (pure functions of hardware.System)
    compare/            speedup, geomean, layer overhead, TCO
    load/               histogram (HDR-style), Scenario/Phase, SLO, knee, recovery
    scan/               can an image/binary run on the target architecture (amd64/arm64)
    result/             schema of the result file "cpubench/v1"
  port/                 ❷ every interface the app uses to talk to the outside
  app/                  ❸ use cases: Run, Stress, PowerCurve, NUMAMatrix, Load, Density, Startup, Scan, RecordPower
  workload/             ❹ the kernels that are measured (not an adapter)
  adapter/in/           ❺ driving: cli, websut (target server), httpapi (agent, hub)
  adapter/out/          ❻ driven: probe/linux, affinity/linux, power, store/jsonfile, report, httpload, hubclient, process, scanner
  archtest/             tests that enforce the dependency rules (instead of depguard)
```

Dependency rules (enforced by `internal/archtest`):

| package | must not import |
|---|---|
| `domain/**` | `os`, `os/exec`, `net/*`, `syscall`, `x/sys`, app, port, adapter, workload |
| `port` | app, adapter |
| `app` | adapter, `os/exec`, `net/http` |
| `workload` | `os`, `net/*`, app, adapter |
| `adapter/out/**` | `adapter/in`, app |

Other conventions:
- **No interface in a hot loop**: `Workload.RunThread` is called once per iteration per thread, and the loop inside is all concrete code
- **Workloads must be deterministic**: a fixed amount of work per call, and the checksum must be identical every time for the same id. Float results are rounded to 6 digits before hashing, because arm64 uses fused multiply-add and amd64 does not
- **Thread pinning**: a goroutine calls `LockOSThread` and then `sched_setaffinity`, and **exits without unlocking**, so the runtime discards the affinity-restricted thread instead of reusing it
- **CPU order**: `PlacementOrder` uses physical cores first (one NUMA node at a time) and only then SMT siblings, so "8 threads" means 8 cores on both x86 and ARM
- **Usable CPUs**: computed from affinity ∩ cgroup cpuset ∩ ceil(cpu.max quota), not from `nproc`
- **Layer**: `System.Layer()` is `Context.Mode` followed by the hypervisor when inside a VM (`vm/kvm`, `vm/vmware`, `container/vmware`). The report and `doctor.Comparable` use this value, not `Mode` alone, because a VM on VMware and one on Proxmox differ in CPU model, scheduler and devices

## How things are measured

| Part | Method |
|---|---|
| micro | 1 warm-up iteration (which also first-touches memory), then N measured iterations with every thread released together by a barrier; reports the median and CV% (above 5% is flagged as noisy) |
| LZMA | `workload/lzma`: range coder, models and state machine of the LZMA1 format, a hash-chain match finder (hash table sized to the dictionary as in 7-Zip's HC4, depth 32, nice 32), greedy parsing with one step of lazy matching, and no end marker. The input (`lzInput`) is made the way 7-Zip's benchmark makes it. `TestStreamIsPinned` pins the stream's CRC so x86-64 and arm64 produce the same bytes, and `TestDecodesXZ` decodes a stream made by xz |
| memory BW | strong scaling: the total working set is fixed and divided among the threads, and each thread first-touches its own part, so the pages sit in the same NUMA node as the thread |
| NUMA matrix | builds a pointer chain on a thread pinned to node M (first-touch), then chases it from node C |
| web | open model: each request has a scheduled send time (intended time) and latency is measured from that time, so there is no coordinated omission. If the load generator falls behind by more than the deadline, the request counts as dropped and the SLO fails |
| knee | Max RPS @ SLO = the highest achieved RPS of a step that passes p99 ≤ SLO, error ≤ SLO and achieved ≥ 95% of target |
| distributed | the coordinator splits the rate equally among agents and sets a common start time, then merges the histograms (this kind of histogram is mergeable) |
| power | Redfish (`/Chassis/*/Power` or `EnvironmentMetrics`), IPMI DCMI, RAPL or hwmon, sampled every 1 second and split by each step's time window |
| TCO | servers = ceil(target / knee) + redundancy and total = capex + W × PUE × 8760 / 1000 × rate × years |
| whole machine | `compare.Whole`: each column uses the result at each throughput workload's highest thread count (excluding single-thread, latency, `sync/atomic-contended`, `sync/mutex`), takes the geomean of the ratios against the baseline, then divides by usable cores (`System.UsableCores`), sockets (only runs that had the whole machine), watts at full load (`Stress.AvgWatts` or the 100% step of the power curve) and price (`server_price` from the TCO, same currency as the baseline) |
| single-thread | `compare.singleThread`: geomean of the speedups of the throughput rows at 1 thread (excluding latency, `sync/atomic-contended`, `sync/mutex`), the same workloads `compare.Whole` takes at the highest thread count; the report puts them side by side as the Single-thread and Multi-thread cards, and `compare` prints them as `single:<label>` and `multi:<label>` lines |
| baseline | `app.DefaultBaseline` uses the first result in the order given and **never chooses by architecture**; `AmbiguousBaseline` returns the machine names when there is more than one, so that the CLI and the report warn you to pass `--baseline` |

## One command / one button

- `app.Auto` strings every stage together (micro → stress → power-curve → density → web) according to `app.Profiles` (smoke/quick/standard/full) and merges them into one result file
- **hub control**: `domain/control.Board` is a state machine with no I/O. It:
  - knows which nodes are connected
  - queues jobs and hands them out one at a time per node
  - pairs web tests: the `web-serve` job on the server must report the `ready` stage before the `web-load` job is released, and when the load finishes the hub tells the server to stop
- the hub (`adapter/in/httpapi`) wraps the Board with a mutex and long-polling, and the node (`adapter/in/cli/node.go`) polls the hub, runs the use case, uploads the result and reports status back
- **Security**:
  - jobs are only the fixed types; the hub cannot send shell commands
  - the API needs a Bearer token; the buttons use Basic auth (password = token) and a same-origin check against CSRF
  - BMC credentials stay on the node
- The SUT reports its own power through `/_cpubench/stats?power=1`, so the load generator can compute RPS/W even when it cannot reach the server's BMC

## Platform data

When comparing x86 machines from different brands that use the same CPU family, the differences usually come from what the vendor put around it, so the probe also records this in `System`:

- `Platform.BIOSVendor`, `BIOS`, `BIOSDate` from `/sys/class/dmi/id`
- `Memory.Slots` (every slot the firmware reports, including empty ones) and `DIMM.Type`, `Configured`, `Rank`, `Manufacturer` from `dmidecode -t 17` (needs root)
- `PCI`: storage and network controllers (PCI class `01xxxx`/`02xxxx`) with driver, NUMA node, current and maximum link speed/width
- `Disks`: block devices with hardware behind them (not loop, dm, md, zram)

Doctor rules that use this data:

| Rule | Condition |
|---|---|
| `dimm-channels` | bare metal with fewer than half the channels per socket populated (servers have at most 2 DIMMs per channel; if every channel has a DIMM, at least half are populated) |
| `dimm-mixed` | DIMMs differ in size, speed or part |
| `dimm-speed` | configured speed is below rated speed |
| `pcie-link` | bare metal with a controller whose link speed or width is below its maximum |
| `virt-mismatch` (`Comparable`) | two columns are in VMs on different hypervisors |

`scripts/capture-fixture.sh` captures these files too. The `arm64-2s-synthetic` fixture has examples of a NIC, SAS, NVMe and a bridge (which must be skipped).

## A copy for sending to a vendor

`app.ShareCopy` (called from `compare --for <label|machine> [--others drop|anonymize]`) builds a new result set without changing the original files:

- keeps every run of the named machine and of the baseline machine
- other machines are dropped (`drop`) or renamed `candidate-a`, ... (`anonymize`), and anything that identifies a vendor or an offered price is removed, both in structured fields and in free text (findings, kernel log); see `anonymize()` for the list
- BMC addresses on every machine in the copy are reduced to the type, and `TCOInput.Only` removes the prices of machines not in the copy
- `TestShareCopyAnonymizesWithoutLeaks` checks that no name, model or price is left in the copy's JSON. If you add a field to `result.File` that can identify a machine, add it to `anonymize()` and to this test

## Memory in containers

- At startup, set the Go soft memory limit (`GOMEMLIMIT`) to 85% of the cgroup `memory.max`, because Go adjusts only GOMAXPROCS to the cgroup, not memory
- The runner skips workloads whose `Info.MemoryMB()` exceeds 50% of min(RAM, memory limit) and records a `mem-limit` finding
- It calls `debug.FreeOSMemory()` after every workload
- All of this was added after a 1 GiB pod was OOM-killed during a test on kind

## Merging result files

Every command writes one `result.File` (schema `cpubench/v1`). `compare` and the hub merge files with the **same label** (for example a micro run and a web load of the same server) and group them by **machine** to compute the overhead of each layer.

`compare --repeats` calls `app.GroupRepeats` after `MergeByLabel`: files that share the machine, `System.Layer()` and `Command` are repeats and fold into one file whose `Runs` lists the runs' labels. Each benchmark is the median of the runs' medians and its `Stats` is the spread between runs; everything else comes from the first run. If the checksums differ between runs the row gets `ChecksumStable=false`, and build or system differences between runs become findings of that column. `anonymize()` replaces `Runs` with aliases too.

## Build info and the crypto backend

A difference in results may come from the binary or the instruction set rather than CPU speed, so both are recorded in the result file.

- **`File.Build`** (`domain/build`): read from `debug.ReadBuildInfo` in `cmd/cpubench`: version (`-X main.version`), commit (`vcs.revision`), modified, Go version, `GOAMD64`/`GOARM64`, cgo, tags and GOEXPERIMENT
  - `build.Self` warns when built from uncommitted source
  - `build.Comparable` warns in `compare` when version/commit differ, when the ISA level differs on the same architecture, when a side does not use the default level (`v1` / `v8.0`), or when tags or GOEXPERIMENT differ
  - files from older versions with no `build` get a `build-unknown` finding (info)
- **`Result.Backend`**: workloads that use `benchmark.BackendReporter` (all crypto) report whether they ran on `hardware` / `simd` / `asm` / `software`
  - it uses the same conditions as `crypto/internal/fips140` in Go 1.26, decided from the flags in `/proc/cpuinfo`
  - the probe must keep the flags those conditions use (`ssse3`, `sse4_1`), which `TestFixturesKeepCryptoFeatures` checks
  - if a newer Go version changes the conditions, update `internal/workload/crypto.go`
- `compare` shows the label `implementation differs` and creates a `backend-differs` finding when the machines run different ways, for example AVX2 versus ARMv8 SHA2
- the doctor rule `ruleISA` lists missing extensions: AES/PMULL, SHA2, SHA512, SVE on arm64 and AES-NI/PCLMULQDQ, SHA-NI on x86

## Testing

- `go test ./...` runs on both amd64 and arm64 (CI runs both) and does not need a machine of the other architecture
- `testdata/fixtures/*`: /proc and /sys trees that the probe reads
  - `x86-wsl-i7-1360p` was captured from a real machine
  - `arm64-2s-synthetic` was built by hand to simulate a 2-socket, 4-NUMA-node arm64 server
- Add a fixture from a real machine with `sudo bash scripts/capture-fixture.sh testdata/fixtures/<name>`
- HTTP code is tested with `httptest` and use cases with fake ports

## Status

Phases 1–5 are done and tested on x86 (WSL2). **What still needs testing on real machines**:

| Topic | What to do |
|---|---|
| arm64 binary | `auto standard` and `auto full` ran to the end on an ARM64 VM (QEMU/KVM), and the report against an x86 VM has no checksum warning<br>**Not yet confirmed**: bare-metal arm64 |
| memory locality on an arm64 VM | If a VM pins only vCPUs and not memory to one NUMA node, part of its memory pages sit on another node (atomics ~9x slower, DRAM ~2x slower), so `sync/atomic-*`, `mem/latency-*` and `mem/triad` swing and cannot be compared. Pin both vCPUs and memory; see `deploy/proxmox/create-bench-vm.sh` and [docs/METHODOLOGY.en.md](docs/METHODOLOGY.en.md) |
| arm64 fixture | Currently synthetic: capture one from a real arm64 server (and a real x86 server) and check that MIDR, NUMA and NIC match |
| Real BMC Redfish | Tested against a mock that follows the Redfish spec; still to try against each vendor's BMC (iDRAC, iLO, XCC, iBMC, …) |
| Proxmox on ARM | The VM creation script has never been run for real (the default is a dry run) |
| Docker | Tested: compose `node` (bridge + `HOST_IP`) → auto/web-test buttons → overhead against a run of the same machine on the lowest layer (against the VM if there is no native run), and the ARM image builds into a tar without QEMU |
| K8s / Helm | Tested on kind (x86, 1 node): Helm mode `node` → pod joins the hub → auto/web-test buttons through a NodePort, Guaranteed QoS. The static CPU manager and a real mixed-arch cluster are not tested yet |
| `full` image for arm64 | Needs buildx + QEMU or a build on an ARM runner |

## Splitting the work (for agents)

Each task below can be done on its own and does not touch the same files:

| Task | Main files | Acceptance |
|---|---|---|
| Add a workload (for example protobuf, brotli, RE2-heavy regex) | `internal/workload/*.go` + register it in `All()` | passes `TestWorkloadsDeterministic` |
| Real-server fixtures (x86 and arm64) + fill in MIDR | `testdata/fixtures/`, `domain/hardware/derive.go` | the tests in `probe/linux` pass with the new fixture |
| A new doctor rule | `domain/doctor/doctor.go` | one unit test per rule |
| Reference stacks (Node/Java/PHP/Python) | `deploy/stacks/<runtime>/` Dockerfile + the same endpoints as `web serve` | `web load --endpoint` works |
| Report: interactive charts | `adapter/out/report` | `TestRenderEverySection` passes and the report still opens offline |
| CI: build the image and push it to GHCR | `.github/workflows/` (test + build-all on amd64/arm64 and the release binary exist already) | both the `slim` and `full` images land in the registry when a tag is pushed |
