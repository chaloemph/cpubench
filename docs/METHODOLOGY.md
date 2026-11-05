# Test methodology

This document explains how cpubench measures a server and how we compare
machines, so that vendors and other users can reproduce a run and read the
report the same way we do. Commands and flags are in the [README](../README.md)
([English](../README.en.md)).

## 1. What question the test answers

The goal is a sizing or migration decision: can the current fleet move to
another server (another vendor, another generation, or the other architecture,
x86-64 or ARM64 in either direction), and how many servers, watts and money
does that take? We answer it in three views, and a report never mixes them:

| View | Compares | Answers |
|---|---|---|
| Per core | Each benchmark at the **same thread count** (1, physical cores, all CPUs) | Is one core of B faster or slower than one core of A? |
| Whole machine | Each machine at **its own maximum** thread count | How much work does one server do, per socket, per watt, per unit of price? |
| Web | Open-model HTTP load until the SLO breaks | How many requests per second does the server sustain within p99 and error limits? |

A per-core result from a small VM says nothing about a whole 2-socket server, and
the other way round. Every report states which view a number belongs to.

## 2. Rules that make two runs comparable

`cpubench doctor` checks most of these and `compare` warns when two columns break
one of them.

1. **Same binary.** Build with `make build-all` from one commit and copy the
   matching binary to every machine. Every result records version, commit, Go
   version and ISA level (`compare` warns on `tool-mismatch`).
2. **Same ISA baseline.** The release binaries are built at the base level of
   each architecture (`GOAMD64=v1`, `GOARM64=v8.0`). Neither side uses
   AVX-512 or SVE/SVE2. This is symmetric, but it is not each CPU's peak.
   Go's crypto still uses AES-NI / SHA-NI / ARMv8 crypto when the CPU has them,
   and the report shows the implementation used on every crypto row.
3. **Same OS and kernel family.** Same distribution release on every machine
   (`os-mismatch`, `kernel-mismatch`).
4. **Same layer.** Compare native with native, VM with VM, container with
   container (`mode-mismatch`). The hypervisor is part of the layer: a VMware VM
   and a KVM VM are different layers (`virt-mismatch`).
5. **CPU frequency policy.** Set the `performance` governor on bare metal
   (`sudo cpupower frequency-set -g performance`); `doctor` warns otherwise.
6. **VM CPU type.** An x86 VM must expose the host CPU (`cpu=host` on Proxmox,
   or the highest EVC mode on VMware). Generic types such as `kvm64` hide AVX2,
   AES-NI and SHA-NI, and crypto and compression numbers become meaningless
   (`vm-cpu-type`).
7. **NUMA placement of VMs.** Pin the vCPUs **and** the guest memory to one
   host NUMA node. Pinning only the vCPUs is not enough: part of the guest's
   memory can land on another node, where atomics were about 9x and DRAM loads
   about 2x slower in our tests on an ARM64 VM, and the memory and concurrency rows
   swing by tens of percent between runs. `deploy/proxmox/create-bench-vm.sh`
   applies these settings (and `balloon=0`, virtio multiqueue).
8. **Memory population.** Bare metal should have every memory channel
   populated with identical DIMMs at the rated speed. `doctor` reports unequal
   channels, mixed DIMMs and DIMMs running below their rated speed.
9. **Nothing else running.** Results are only valid when the machine is idle
   apart from cpubench. On Kubernetes, use Guaranteed QoS and the static CPU
   manager so the pod gets whole cores.
10. **Repeat.** Run the decision profile at least 2-3 times per machine. The
    report shows the spread when several runs share a machine, and warns when a
    column was measured once.

## 3. What one run measures

`cpubench auto --profile <p>` runs every stage below on one machine and writes
one JSON result plus an HTML report.

| Profile | Time | Iterations | Thread counts | Stress | Power curve |
|---|---|---|---|---|---|
| `smoke` | ~1 min | 2 (5% size) | 1, all | 15 s | 0, 100% |
| `quick` | ~5 min | 3 (30% size) | 1, all | 2 min | 0, 50, 100% |
| `standard` | ~25 min | 5 | 1, physical cores, all | 10 min | 0-100% in 25% steps |
| `full` | ~1.5 h | 5 | 1, 2, 4 … physical, all | 30 min | 0-100% in 25% steps |

`standard` is the profile for decisions. `smoke` only proves the installation
works.

### 3.1 Micro suite (`run`)

Each benchmark returns a checksum; two machines must produce the same checksum
for a row to be compared (float rows may differ in the last bits and are
flagged, not dropped). Threads are placed on physical cores first, one NUMA node
at a time, then on SMT siblings.

| Category | Benchmarks | Stands for |
|---|---|---|
| integer | `int/json`, `int/regex`, `int/sort`, `int/hashmap`, `int/compress`, `int/decompress` | Typical application logic: parsing, matching, maps, compression |
| float | `float/matmul`, `float/nbody`, `float/fft` | Numeric code |
| crypto | `crypto/aes-gcm`, `crypto/sha256`, `crypto/ecdsa-p256`, `crypto/rsa2048-sign` | TLS bulk encryption, hashing, TLS handshakes |
| memory | `mem/triad`, `mem/copy` (bandwidth), `mem/latency-32K … 512M` (pointer chase), NUMA matrix (`standard`/`full`) | Cache and DRAM behaviour, cross-node cost |
| concurrency | `sync/atomic-local`, `sync/atomic-contended`, `sync/mutex`, `sync/pingpong` | Lock-heavy code, cross-core communication |

Categories are summarised with a geometric mean of the per-row ratios, and the
report always shows the lowest and highest row next to it. A category mean hides
large differences (for example SHA-256 and RSA can move in opposite directions),
so read the rows before quoting a category.

### 3.2 Stress (`stress`)

All threads at full load for the profile's duration. It reports sustained versus
peak throughput, clock, temperature, power, throttling, checksum errors and
machine-check (MCE) messages from the kernel log. Its "iterations per second"
mixes benchmarks of different lengths, so use it for **stability**, not as a
speed ratio.

### 3.3 Power (`power-curve`, `--power`)

Power is read from the BMC over Redfish (`redfish://user@bmc`), IPMI, RAPL or
hwmon, at 0/25/50/75/100% load. A VM cannot read its host's power: run
`cpubench power` on a control machine in parallel instead. Per-watt numbers in
the whole-machine view come from the stress average or the 100% power-curve
step, so a run without `--power` has no per-watt column.

### 3.4 Density (`density`)

How many instances of a fixed size (8 threads by default, the size of a typical
VM or pod) the machine runs while each still gets at least 90% of its solo
speed.

### 3.5 Web (`web serve` + `web load`)

`web serve` is a built-in HTTP target with the same code on every architecture.
`web load` is an open-model load generator: it sends requests at a fixed rate,
whether or not earlier ones have returned, so slow servers show up as latency
and errors instead of a lower request rate.

- **Ramp** (default): rate steps up until p99 latency (100 ms default) or the
  error rate (0.1% default) breaks the SLO. `max RPS @ SLO` is the highest step
  that met the SLO; it is only as fine as the step size.
- **mix** request preset: 40% JSON, 22% cache read, 3% cache write, 15% HTML
  template, 10% gzip, 7% JSON parse of a POST body, 3% login (PBKDF2 password hash).
  `plaintext` is also available but usually saturates the load generator
  first.
- The load generator must not be the bottleneck. Run it on a separate machine
  with enough cores, or split one machine into server CPUs and load CPUs
  (`tools/web-loopback.sh` puts the server on CPUs 0-3 and the load on 4-7). A
  step only counts when the **server** CPUs saturate first; the report warns
  when the load generator saturated before the server.
- A loopback test compares server CPUs only. It leaves out the NIC and virtio
  cost, which a test across the real network includes.
- `web load --scenario` replays your own application's requests
  (`examples/scenario-*.yaml`).

## 4. Testing layer by layer

Run every layer of one machine with the same `--machine` and a different
`--label` (`arm-01-native`, `arm-01-vm`, `arm-01-docker` …). The report
then shows each layer's overhead against the lowest layer of that machine.

| Layer | How | Notes |
|---|---|---|
| Bare metal | binary, `auto` | The only layer for per-socket and per-watt numbers |
| VM | binary inside the VM | Rules 6 and 7 above; read power on the host side |
| Docker | `deploy/docker/compose.yaml` | Compare with native **on the same VM/host** to isolate the container overhead |
| Kubernetes | `deploy/k8s/`, `deploy/helm/cpubench` | Guaranteed QoS, static CPU manager; check the worker's own NUMA placement first |

## 5. Reading the report

The `compare` report is ordered **conclusion → conditions → numbers → what this
report cannot answer**. Read the warnings before any number. The most common
ones:

| Warning | Meaning |
|---|---|
| checksum differs (outside float) | The two machines did not run the same work; the row is not comparable |
| implementation differs (crypto) | One side has a hardware instruction the other lacks; the row compares instruction sets, not CPUs |
| load generator saturated | The web number is the generator's ceiling, not the server's |
| different layers / different CPU counts | The columns are not like for like |
| single run | No spread is available; repeat before quoting the number |
| baseline picked automatically | Pass `--baseline <label>` with the machine you run today |

## 6. What we ask from a vendor

To test a candidate server under the same conditions as the current fleet:

1. Bare metal access to the server model being offered, configured as it would
   be delivered (CPU, every memory channel populated, BIOS version and
   settings written down).
2. A BMC user with Redfish read access to power readings.
3. The same OS release as our baseline (Ubuntu LTS unless agreed otherwise).
4. Permission to run the `standard` profile at least three times, plus `full`
   once, and a web test with a separate load generator.
5. If the server is offered as a VM platform as well, the hypervisor settings
   for CPU type and NUMA placement.

Each vendor receives a copy of the report that contains only their machine and
our baseline (`cpubench compare --for <label>`). Other vendors' machines are
either removed or anonymised, and their prices are never included.

## 7. Known limits

- Synthetic benchmarks are a proxy. Confirm with your own application through
  `web load --scenario` and `startup` before a final decision.
- Inside a VM the clock speed is often hidden (on the ARM64 VMs we tested it
  could not be read at all), so a VM result cannot separate clock from IPC.
- A VM result does not stand in for a different CPU model of the same vendor.
  `cpubench info` shows the ARM MIDR part number; compare it with the CPU you
  plan to buy.
