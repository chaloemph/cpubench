# cpubench

[ภาษาไทย](README.md) · English

A CPU and web-workload benchmark for comparing x86-64 (amd64) and ARM64
servers, with no ties to a vendor or CPU model. Use it across architectures
(x86 ↔ ARM, either direction) or between machines of the same architecture from
several vendors.

- One static binary with no runtime dependencies (linux/amd64, linux/arm64)
- Runs native, in a VM, in Docker and on Kubernetes, and reports the overhead of each layer
- Builds self-contained HTML and Markdown reports (Thai or English)
- A hub with run buttons collects the results from every machine

How we test and how to read the numbers: **[docs/METHODOLOGY.md](docs/METHODOLOGY.md)**.

## Install

Download `cpubench-linux-amd64` or `cpubench-linux-arm64` from
[Releases](https://github.com/chaloemph/cpubench/releases) and check it against
`SHA256SUMS`, or build it:

```bash
go install github.com/chaloemph/cpubench/cmd/cpubench@latest   # Go 1.26+
make build-all          # dist/cpubench-linux-amd64, dist/cpubench-linux-arm64
make image              # multi-arch image (slim + full), needs docker buildx
make test
```

Use binaries built from **one commit** on every machine; `compare` warns when
they differ.

## One command per machine

```bash
cpubench auto --profile standard --label arm-01 --power redfish://admin@arm-01-bmc --upload http://hub:8090
```

| Profile | Time | Use |
|---|---|---|
| `smoke` | ~1 min | Check the installation and connections |
| `quick` | ~5 min | A rough look |
| `standard` | ~25 min | Results for decisions |
| `full` | ~1.5 h | Fine thread sweep + 30 min stress |

It writes one JSON file and `cpubench-<label>-<profile>.html`. When every
machine has run, compare them against the machine you run today (x86 or ARM):

```bash
cpubench compare --latest --baseline x86-01 results/
```

cpubench never prefers an architecture. Without `--baseline` it takes the first
result given, and when there are several machines it warns on stderr and at the
top of the report.

## One button through the hub

```bash
cpubench hub --token <token>                                                       # control machine
cpubench node --hub http://hub:8090 --token <token> --power redfish://admin@<bmc>  # every machine under test
```

Open `http://hub:8090`:

- **smoke / quick / standard / full** per machine, or **Run on every online machine**
- **Web test**: pick a server and a load generator; the hub starts `web serve`, runs the load and stops the server
- **Compare latest of every machine**: a report of every machine straight away (fill in Baseline with the machine in production)

The buttons ask for a password (any username, the token as password). BMC
passwords stay on each node and never pass through the hub. To keep a node
running, see `deploy/systemd/cpubench-node.service`. Read
[SECURITY.md](SECURITY.md) before you expose a hub.

## Step by step

Repeat the same steps on **every machine you compare** (for example `x86-01`
and `arm-01`), with the same OS release on all of them.

```bash
# 0. Check the machine
cpubench info
cpubench doctor                      # governor, NUMA, DIMMs, VM CPU type, ...
sudo cpupower frequency-set -g performance

# 1. Micro suite (10-20 min on a large machine)
cpubench run --label arm-01 --threads 1,8,32,max --upload http://hub:8090

# 2. Stress: sustained vs peak, throttling, errors
cpubench stress --label arm-01 --duration 30m --power redfish://admin@arm-01-bmc

# 3. Power curve for TCO (read from the BMC)
export CPUBENCH_BMC_PASSWORD=...
cpubench power-curve --label arm-01 --power redfish://admin@arm-01-bmc

# 4. Web: target on the server, load from another machine
cpubench web serve --addr :8080                                     # on the server
cpubench web load --target http://arm-01:8080 --label arm-01 \
  --power redfish://admin@arm-01-bmc --upload http://hub:8090   # on the load generator

# 5. Compare (the same label merges micro + web results of one machine)
cpubench compare --baseline x86-01 --tco examples/tco.yaml -o report.html --md report.md \
  results/*.json
```

### Layer by layer (native / VM / Docker / K8s)

Give every run of one machine the same `--machine` and a different `--label`;
the report shows each layer's overhead against native.

```bash
cpubench run --machine arm-01 --label arm-01-native          # bare metal
cpubench run --machine arm-01 --label arm-01-vm              # in a Proxmox VM
docker run --rm cpubench:latest run --machine arm-01 --label arm-01-docker --upload http://hub:8090
kubectl apply -f deploy/k8s/micro-job.yaml                         # or helm (deploy/helm/cpubench)
```

The hypervisor is part of the layer: a VMware VM is `vm/vmware`, a Proxmox VM
is `vm/kvm`, and the report warns (`virt-mismatch`) when columns cross
hypervisors. A VM cannot read its host's power; run
`cpubench power --power redfish://... --duration 20m --label arm-01-vm` on a
control machine at the same time.

### Your own application

```bash
cpubench web load --scenario examples/scenario-real-app.yaml --label app-arm-01 --sut-stats=false
cpubench startup --cmd "node server.js" --ready http://127.0.0.1:3000/health
cpubench density --workload int/json --group 8          # VMs/pods of 8 vCPU per machine
cpubench scan --arch arm64 --image registry.local/shop/api:1.4 --dir ./node_modules   # or --arch amd64
```

## Commands

| Command | Does |
|---|---|
| `info`, `doctor` | CPU (MIDR on ARM), NUMA, SMT, cgroup, context, BIOS, DIMM population, PCIe links of storage/network controllers, disks, and settings that skew results |
| `run` | Micro suite: integer, float, crypto, memory (bandwidth, latency ladder, NUMA matrix), concurrency |
| `stress` | Full load on every thread: sustained vs peak, MHz, °C, W, throttling, checksum/MCE errors |
| `power-curve` | Power at 0/25/50/75/100% utilisation |
| `power` | Read power (test BMC access) or record a trace while a VM runs |
| `web serve` / `web load` / `web agent` | Built-in web target and an open-model load generator (ramp→SLO, constant/soak, stress, spike, distributed) |
| `density` | Instances that still get ≥90% of their solo speed |
| `startup` | Application start time and JIT warm-up |
| `scan` | Whether images and binaries/wheels/jars/node addons have builds for the target architecture (`--arch amd64` or `arm64`) |
| `hub` | Collects results from every machine; compare in the browser |
| `compare` / `report` | HTML report (one file, works offline) + Markdown + TCO table |

Power sources (`--power`): `redfish://user@bmc`, `ipmi://local`,
`ipmi://user@bmc`, `rapl`, `hwmon[:name]`, `const:W` (the password is read from
`$CPUBENCH_BMC_PASSWORD`).

## Reading the report

The report goes **conclusion → conditions → numbers → what it cannot answer**.
Read the warnings before any number; see
[docs/METHODOLOGY.md §5](docs/METHODOLOGY.md#5-reading-the-report). Set the
language with `cpubench compare --lang th|en` (default `th`).

The **whole machine** section compares each machine at its own maximum thread
count, per machine, per core, per socket (bare metal only), per watt (needs
`--power`) and per price (`server_price` from `--tco`, same currency as the
baseline). Use it when machines have different core counts.

## A copy for one vendor

The normal report contains every vendor's results. To send it to one vendor,
make a copy with `--for`:

```bash
cpubench compare --baseline current-vm --for vendora01 --lang en -o report-vendora.html results/*.json                     # drop other machines
cpubench compare --baseline current-vm --for vendora01 --others anonymize --lang en -o report-vendora.html results/*.json  # keep them, anonymised
```

- `--for` takes labels or machines, comma separated; the baseline's runs are always kept
- `--others drop` (default) removes every other machine; the safest choice
- `--others anonymize` renames other machines `candidate-a`, `candidate-b`, … and removes hostnames, CPU models, features, platform, BIOS, DIMM and disk part numbers, PCI devices, URLs, BMC addresses and prices, including inside finding texts and kernel log lines
- Other machines' prices never leave, and every BMC address is reduced to its type (`redfish`, `ipmi`)
- The architecture stays visible: when only one vendor offers an architecture, its anonymised column still identifies them
- Only `compare` supports this; the hub's compare button does not

## Deploy

- `deploy/docker/compose.yaml`: node, SUT, load generator, micro, hub ([docs/DOCKER.md](docs/DOCKER.md))
- `deploy/k8s/`: Job for micro, Deployment + NodePort for the web SUT, hub
- `deploy/helm/cpubench`: chart with `mode` (node/auto/micro/stress/sut/hub) and `arch` ([docs/K8S.md](docs/K8S.md))
- `deploy/proxmox/create-bench-vm.sh`: VM with `cpu=host`, NUMA binding, no balloon, virtio multiqueue, UEFI on ARM (dry-run by default)
- `scripts/collect-info.sh`, `scripts/capture-fixture.sh`: collect machine info / test fixtures

Install guides (Thai): [bare metal + hub](docs/INSTALL.md) · [Docker](docs/DOCKER.md) · [Kubernetes](docs/K8S.md).
Architecture and how to extend it (Thai): [DESIGN.md](DESIGN.md).

## License

[Apache License 2.0](LICENSE)
