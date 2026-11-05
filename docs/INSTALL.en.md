# Install cpubench hub + node (x86-64 and ARM64)

[ภาษาไทย](INSTALL.md) · English

Goal: install once, then start every test from buttons on the hub web page.
Installation takes about 30–45 minutes (mostly BIOS and BMC settings).

## 0. Lay out the machines

```
                  ┌───────────────────────────────┐
                  │ lg01 (load generator + hub)   │  a third x86 machine, ≥16 cores, NIC ≥10GbE
                  │   cpubench hub   :8090        │
                  │   cpubench node  (lg01)       │
                  └───────────────┬───────────────┘
                     same switch  │
          ┌───────────────────────┴───────────────────────┐
┌─────────┴──────────────┐                    ┌───────────┴────────────┐
│ arm-01 (ARM, SUT)      │                    │ x86-01 (x86, SUT)      │
│  cpubench node :8080   │                    │  cpubench node :8080   │
│  BMC → Redfish/IPMI    │                    │  BMC → Redfish/IPMI    │
└────────────────────────┘                    └────────────────────────┘
```

| Role | Machine | Why |
|---|---|---|
| **SUT** (server under test) | Every machine you compare (this guide uses `arm-01` and `x86-01`; it works for any vendor, and both can be x86 or both ARM) | Same Ubuntu release, similar kernel and the same BIOS settings on all of them |
| **Load generator** | A third machine (recommended) | If the SUTs load each other, the machine that sends load competes for CPU with the work being measured and the results skew |
| **Hub** | Can share the load generator's machine | It needs very little, and port 8090 does not clash with `web serve` on 8080 |

With only 2 machines you can still run the micro, stress and power tests in full.
For the web test let one machine load the other and swap, and treat the web
numbers as estimates.

**Ports to open on the test network:**

| From → to | Port | Used for |
|---|---|---|
| every node → hub | 8090/tcp | poll for jobs and upload results |
| your browser → hub | 8090/tcp | the hub web page |
| lg01 → SUT | 8080/tcp | web test |
| SUT → its own BMC | 443/tcp (Redfish) or 623/udp (IPMI) | read power |

## 1. Prepare every machine (all SUTs and lg01)

### 1.1 OS and packages

```bash
sudo apt update && sudo apt install -y numactl dmidecode ipmitool ethtool chrony linux-tools-common linux-tools-$(uname -r)
```

- On Ubuntu 22.04 also install the HWE kernel so it has kernel 6.8 like 24.04, then reboot: `sudo apt install -y linux-generic-hwe-22.04`
- `chrony` keeps clocks in sync; it is required when load comes from several machines

### 1.2 BIOS / BMC (set identically on every SUT)

| Setting | Value |
|---|---|
| Power policy | **Performance** or OS-controlled (not Energy saving) |
| SMT / Hyper-Threading | The same on every machine (some CPUs have no SMT; it is recorded in the result) |
| Memory | Every channel populated, identical DIMMs on every socket |
| Redfish | Enabled, with a user who can read Power (iDRAC, iLO, XClarity, iBMC, OpenBMC, …) |
| IPMI over LAN | Enabled, if you will use `ipmi://` instead of Redfish |

### 1.3 CPU governor (on the SUTs)

```bash
sudo cpupower frequency-set -g performance
```

## 2. Install the binary

Build on a development machine that has this repository:

```bash
make build-all
```

This produces `dist/cpubench-linux-amd64`, `dist/cpubench-linux-arm64` and `dist/SHA256SUMS`. Copy them to every machine:

```bash
scp dist/cpubench-linux-arm64 dist/SHA256SUMS deploy/systemd/*.service scripts/collect-info.sh ubuntu@arm-01:/tmp/
```
```bash
scp dist/cpubench-linux-amd64 dist/SHA256SUMS deploy/systemd/*.service scripts/collect-info.sh ubuntu@x86-01:/tmp/
```
```bash
scp dist/cpubench-linux-amd64 dist/SHA256SUMS deploy/systemd/*.service ubuntu@lg01:/tmp/
```

In the later steps, wherever a command mentions `deploy/systemd/...` or `scripts/...`, use the copies in `/tmp/` instead.

On each machine, check the checksum and install (the command picks the architecture for you):

```bash
cd /tmp && sha256sum --ignore-missing -c SHA256SUMS && sudo install -m755 cpubench-linux-$(dpkg --print-architecture) /usr/local/bin/cpubench && cpubench version
```

## 3. Install the hub (on lg01)

```bash
echo "CPUBENCH_TOKEN=$(openssl rand -hex 16)" | sudo tee /etc/cpubench-hub.env >/dev/null && sudo chmod 600 /etc/cpubench-hub.env && sudo cat /etc/cpubench-hub.env
```

**Write the token down.** You need it to configure every node, and it is the password for the buttons on the web page.

```bash
sudo cp deploy/systemd/cpubench-hub.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now cpubench-hub && systemctl status cpubench-hub --no-pager
```

(If lg01 has no copy of the repository, copy `deploy/systemd/*.service` there in step 2.)

Test it: open `http://<lg01-ip>:8090` in a browser. You should see the "cpubench hub" page with no machines connected yet.

## 4. Install a node on the first SUT (`arm-01`)

### 4.1 Check the machine first

```bash
sudo cpubench doctor
```

Fix every `warn` first (governor, DIMMs, NUMA/NIC) and keep the machine details for your team:

```bash
sudo bash scripts/collect-info.sh
```

### 4.2 Check that power can be read from the BMC

```bash
export CPUBENCH_BMC_PASSWORD='<BMC password>'
```
```bash
cpubench power --power redfish://<bmc-user>@<bmc-ip>
```

You should see something like `redfish https://<bmc-ip>: 245.0 W`. If not, try IPMI instead (enable IPMI over LAN in the BMC first):

```bash
cpubench power --power ipmi://<bmc-user>@<bmc-ip>
```

Or use in-band IPMI from the machine itself (no BMC IP needed):

```bash
sudo cpubench power --power ipmi://local
```

### 4.3 Configure and start the service

```bash
printf 'CPUBENCH_TOKEN=%s\nCPUBENCH_BMC_PASSWORD=%s\n' '<token from step 3>' '<BMC password>' | sudo tee /etc/cpubench.env >/dev/null && sudo chmod 600 /etc/cpubench.env
```

```bash
sudo cp deploy/systemd/cpubench-node.service /etc/systemd/system/ && sudo systemctl edit --full cpubench-node
```

Change the `ExecStart` line to:

```
ExecStart=/usr/local/bin/cpubench node --hub http://<lg01-ip>:8090 --name arm-01 --power redfish://<bmc-user>@<bmc-ip>
```

```bash
sudo systemctl enable --now cpubench-node && journalctl -u cpubench-node -n 20 --no-pager
```

The log should contain `node "arm-01" (arm64, ...) connected to http://<lg01-ip>:8090`.

## 5. Install a node on the other SUTs (`x86-01`, …)

Do exactly the same as step 4. Only these differ:
- use `--name x86-01`
- point `--power` at that machine's BMC

> ⚠️ **Measure power the same way on every machine.** Use the BMC (whole machine) everywhere. Do not mix it with `rapl` (x86 only, and it measures only the CPU package), or the machines that use rapl will look more efficient than they are.

## 6. Install a node on the load generator (lg01)

The load generator does not measure power, so leave out `--power`, but tune the network for many connections:

```bash
printf 'net.core.somaxconn=65535\nnet.ipv4.ip_local_port_range=1024 65535\nnet.ipv4.tcp_tw_reuse=1\n' | sudo tee /etc/sysctl.d/90-cpubench.conf && sudo sysctl --system
```

Do this on every SUT too. Then set up the service:

```bash
printf 'CPUBENCH_TOKEN=%s\n' '<token>' | sudo tee /etc/cpubench.env >/dev/null && sudo chmod 600 /etc/cpubench.env
```
```bash
sudo cp deploy/systemd/cpubench-node.service /etc/systemd/system/ && sudo systemctl edit --full cpubench-node
```
```
ExecStart=/usr/local/bin/cpubench node --hub http://127.0.0.1:8090 --name lg01
```
```bash
sudo systemctl enable --now cpubench-node
```

## 7. Test the system (smoke takes about 5 minutes)

Open `http://<lg01-ip>:8090`:

1. The **Machines** table must list `arm-01`, `x86-01` and `lg01` as **online**, and arm-01 and x86-01 must show power = yes
2. Choose **smoke** and press **Run on every online machine**. The browser asks for a password: enter any username and use the token as the password
3. Wait until every job is `done` (the page refreshes by itself every 5 seconds)
4. In **Web test**, choose Server = `arm-01`, Load generator = `lg01`, profile = **smoke**, and press **Run web test**
5. Press **Compare latest of every machine**. You should see a report with every section

If all 5 pass, the installation is correct.

## 8. Run the real test

| Order | Press | Approx. time |
|---|---|---|
| 1 | **standard** on arm-01 and x86-01 (they can run at the same time because they are different machines) | ~25 min |
| 2 | **Web test** server = arm-01, load generator = lg01, profile = **standard** | 10–20 min |
| 3 | **Web test** server = x86-01, load generator = lg01, profile = **standard** | 10–20 min |
| 4 | **Compare latest of every machine** | report right away |

For a TCO table, pull the results from the hub and build the report with the CLI:

```bash
mkdir -p hub-results && curl -s http://<lg01-ip>:8090/api/results | python3 -c 'import json,sys;[print(r["id"]) for r in json.load(sys.stdin)]' | while read id; do curl -s -o hub-results/$id.json http://<lg01-ip>:8090/api/results/$id; done
```
```bash
cpubench compare --latest --baseline <the machine you run today> --tco examples/tco.yaml -o report.html --md report.md hub-results/
```

(Edit the prices in `examples/tco.yaml` and use `arm-01` / `x86-01` as the keys so they match the node names.)

## 8.1 If the machines you compare are in different places (sites / clusters / networks)

The comparison only needs **the JSON result files of every machine**. They need not be on one hub or one network. Pick one of 3 ways:

| Way | Use when | How |
|---|---|---|
| **A. One hub** | Every place can reach the hub (nodes only need outbound HTTP to port 8090) | Install as in steps 3–7 |
| **B. One hub per place** | The networks are not connected, but your own machine can reach both hubs | Install a hub + load generator at each place, then merge the results with one command (below) |
| **C. No network between them at all** | Isolated networks | Copy the JSON files out (`/var/lib/cpubench/results/` or `/var/lib/cpubench-hub/`) and run `compare` on any machine |

Way B: pull the results from both hubs and build the report:

```bash
cpubench compare --latest --baseline <the machine you run today> --tco examples/tco.yaml -o report.html --md report.md http://<hub-site-a>:8090 http://<hub-site-b>:8090
```

Way C:

```bash
cpubench compare --latest --baseline <the machine you run today> -o report.html results-site-a/ results-site-b/
```

**When you test in different places, control more things than usual** (the report warns when it finds a difference):

| Must be equal | Does the report check it? |
|---|---|
| Ubuntu version, kernel, cpubench version | ✅ `os-mismatch`, `kernel-mismatch`, `tool-mismatch` |
| Layer type (native / VM / Docker / K8s) | ✅ `mode-mismatch` |
| **Same load generator spec** (or carry the same machine to every place) | ✅ `loadgen-differs`, and the environment table shows which load generator was used |
| Load generator equally close to the server (same switch, same NIC speed) | ⚠️ Check it yourself from the **p50 of the first step** of the web test. A difference of several ms means the networks differ |
| Profile, SLO and VM / pod / container size | ⚠️ Use the same buttons and values everywhere |
| Time of the test (nothing else running) | ⚠️ Look at CV%. If it is noisy, test again |

**Micro, stress and power-curve results do not depend on the network**, so they compare directly across places. Only the web result needs care. The layer comparison (native/VM/Docker/K8s) is within one machine, so being in different places does not affect it.

## 9. Measure a VM / container (overhead of each layer)

Install a node inside a Proxmox VM on `arm-01` (create the VM with `deploy/proxmox/create-bench-vm.sh`), give the node a name different from the host, and set `--machine` to the host's name:

```
ExecStart=/usr/local/bin/cpubench node --hub http://<lg01-ip>:8090 --name arm-01-vm --machine arm-01 --power redfish://<bmc-user>@<bmc-ip>
```

- The VM can read the host's power over Redfish through the network, if the VM can reach the BMC
- After you press **standard** on `arm-01-vm`, the report shows a "Virtualisation / container overhead" section with how much slower the VM is than native
- Docker and K8s work the same way; see the examples in `deploy/docker` and `deploy/k8s`

## 10. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| The node does not appear in the hub | `journalctl -u cpubench-node` → `401` means the token differs; `connection refused` means check the firewall on port 8090 and the hub URL |
| The node shows offline | The node has not polled for over 60 seconds. Check that the service is still running |
| A button gives `cross-site request refused` | Open the web page through the hub's own URL, not through a proxy that changes the Host header |
| `redfish: GET /redfish/v1/Chassis: 401` | The BMC user has no Redfish permission, or the password is wrong |
| `redfish: no chassis reports power` | The BMC firmware may have no Power resource. Use `ipmi://` instead |
| The web test fails at the first step | Check that lg01 can reach `http://<sut-ip>:8080/healthz` and that the SUT's firewall allows port 8080 |
| The web test has a lot of `dropped` | The load generator cannot keep up. Use a stronger machine or add agents (`cpubench web agent` + `web load --agents`) |
| Results swing a lot (CV > 5%) | Run `doctor`; check the governor, the power policy and other work on the machine |
| `checksum differs` on a float workload | arm64 uses fused multiply-add, which changes the result slightly. This is normal, but if it happens on int or crypto, investigate |

## 11. Security

- The hub can only send the fixed job types (auto, web-serve, web-load). **It cannot run shell commands on a node**
- The token is in `/etc/cpubench*.env` (mode 600) and the BMC password stays on the node; it is never sent through the hub
- The node service runs as root so that it can read DIMM details, use `ipmi://local` and pin CPUs. If you do not want that, add `User=` to the unit, but you lose the DIMM data
- Use it on a test network only. The hub has no TLS. If it must be reachable from outside the network, put a reverse proxy with HTTPS in front of it (see [SECURITY.md](../SECURITY.md))
- When the tests are done, stop everything with `sudo systemctl disable --now cpubench-node cpubench-hub`
