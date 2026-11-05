# Testing on Docker

[ภาษาไทย](DOCKER.md) · English

The question this test answers: **how does a web app perform in a container, compared between the machines you may choose (x86-64 or ARM64 from any vendor) and against the bare metal of the same machine?** It also shows the overhead of bridge networking (docker-proxy/NAT) against `--network host`.

It uses the same hub and buttons as [INSTALL.en.md](INSTALL.en.md); the only difference is that the node runs in a container.

```
 hub (lg01:8090) ◄── poll ── container "arm-01-docker"         (bridge, -p 8080, 8 CPU, 8 GiB)
                 ◄── poll ── container "arm-01-docker-hostnet" (network host)
 lg01 ── web test ──► <arm-01-ip>:8080 ──► docker-proxy ──► container :8080
```

Every container gets `--machine arm-01`. If you also ran bare metal on `arm-01`, the report shows **Docker vs native** automatically.

> If the machines you compare are at different sites or clusters, see [INSTALL.en.md section 8.1](INSTALL.en.md#81-if-the-machines-you-compare-are-in-different-places-sites--clusters--networks) for how to merge results from several hubs into one report.

## 1. What you need

- Docker Engine + the compose plugin on every SUT (**the same version**)
- A hub installed as in INSTALL.en.md step 3, reachable from the container at `http://<lg01-ip>:8090`
  - **Do not use `localhost`**: inside a container it means the container itself
- The file `deploy/docker/compose.yaml` on each machine

## 2. Get the image onto the machine

**Option 1: you have a registry**

```bash
make image-push IMAGE=registry.local/cpubench
```

Every machine uses `CPUBENCH_IMAGE=registry.local/cpubench:latest`, and Docker pulls the architecture that matches the machine.

**Option 2: no registry** (build the images of both architectures on any machine, without QEMU)

```bash
make image-tar
```
```bash
scp dist/cpubench-image-arm64.tar deploy/docker/compose.yaml ubuntu@arm-01:~/
```
```bash
ssh ubuntu@arm-01 'docker load -i cpubench-image-arm64.tar && docker tag cpubench:latest-arm64 cpubench:latest && docker image inspect cpubench:latest --format "{{.Architecture}}"'
```

The result must be `arm64`. On an amd64 machine do the same with `cpubench-image-amd64.tar` and the tag `cpubench:latest-amd64`.

## 3. Way A (recommended): the container joins the hub and you press buttons

On **arm-01** (in the folder that has `compose.yaml`):

```bash
export HUB=http://<lg01-ip>:8090 CPUBENCH_TOKEN='<hub token>' MACHINE=arm-01 HOST_IP=<arm-01-ip> CPUS=8 MEMORY=8g POWER=redfish://<bmc-user>@<bmc-ip> CPUBENCH_BMC_PASSWORD='<BMC password>'
```
```bash
docker compose --profile node up -d && docker compose --profile node logs --tail 5
```

You must see the line `node "arm-01-docker" ... connected to http://<lg01-ip>:8090 · web tests reach it at <arm-01-ip>:8080`.

To measure the bridge-network overhead too, also start a host-network container:

```bash
WEB_PORT=8081 docker compose --profile node-hostnet up -d
```

Repeat on **x86-01** with `MACHINE=x86-01`, that machine's `HOST_IP` and BMC, and **the same CPUS / MEMORY as the first machine**.

| Variable | Meaning |
|---|---|
| `HUB` | The hub URL (an IP the container can reach) |
| `MACHINE` | The real machine's name, used to group with the bare-metal results |
| `HOST_IP` | The machine's IP that lg01 uses for the web test (the container's own 172.x address is not reachable from outside) |
| `CPUS`, `MEMORY` | Container size. Set it to the size you will really use in production, and the same on every machine |
| `POWER` | Power source (Redfish over the network works from a container) |
| `WEB_PORT` | The port published for the web test (default 8080) |
| `NODE_NAME` | The name on the hub (default `<MACHINE>-docker`) |

Then open `http://<lg01-ip>:8090`:

1. Press **smoke** on `arm-01-docker` and `x86-01-docker` to check everything works
2. Press **standard** on both
3. **Web test**: Server = `arm-01-docker`, Load generator = `lg01`, then repeat with `x86-01-docker` (and `-docker-hostnet` if you started it)
4. Press **Compare latest of every machine** (set Baseline to the machine you run today). You get both the **comparison between machines** and **Docker / Docker-hostnet vs native**

### Pin the CPUs to one NUMA node (recommended on machines with several NUMA nodes)

`CPUS=8` is a quota limit, but the threads can still run on every core. To behave like a pod that gets dedicated cores, enable these 2 lines under `x-limits` in `compose.yaml`:

```yaml
  cpuset: "0-7"     # the CPUs of NUMA node 0; see them with: cpubench info or numactl -H
  cpuset_mems: "0"
```

## 4. Way B: without a hub (one command)

```bash
docker run --rm --cpus 8 --memory 8g --user root -v "$PWD/results:/work/results" cpubench:latest auto --profile standard --label arm-01-docker --machine arm-01
```

The result file lands in `./results/`. Then run the web test separately:

```bash
docker run -d --name cpubench-sut --cpus 8 --memory 8g -p 8080:8080 cpubench:latest web serve        # on arm-01
```
```bash
cpubench web load --target http://<arm-01-ip>:8080 --label arm-01-docker --machine arm-01 --adaptive   # on lg01
```
```bash
docker rm -f cpubench-sut
```

Merge everything with:

```bash
cpubench compare --latest results/
```

## 5. Check the setup

```bash
docker compose --profile node exec node cpubench info
```

The `Context` line must be `container · effective CPUs 8 ... quota 8.00 cores`, matching `CPUS` (with cpuset you also see `allowed 0-7`).

Findings you may see in the report, and what they mean:

| Finding | Meaning |
|---|---|
| `cgroup-limit` | Information only: how much CPU the container is limited to |
| `cgroup-throttled` | The quota throttled the run. If it is high, use `cpuset` instead of `CPUS` alone |
| `mem-limit` | Workloads too large for the container's memory were skipped. Raise `MEMORY` (2–4 GiB or more) if you want the full set |

## 6. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| The node does not appear in the hub | `HUB` is `localhost` or an IP the container cannot reach. Use lg01's real IP and check the token |
| Warning `running in a container; web tests will dial 172.x` | `HOST_IP` was not set |
| The web test fails at the first step | lg01 cannot reach `http://<HOST_IP>:<WEB_PORT>/healthz`. Check the firewall and that the port is published |
| `exec format error` | Wrong image architecture. Check with `docker image inspect cpubench:latest --format '{{.Architecture}}'` |
| A job shows `node lost the job` | The container restarted during the run. Check with `docker inspect <container> --format '{{.State.OOMKilled}}'`; if true, raise `MEMORY` |
| `permission denied` writing `./results` | The slim image runs as a non-root user. When you mount a host folder, add `--user root` (compose already sets it) |
| Power cannot be read with `ipmi://local` | You need the `full` image + `--device /dev/ipmi0` + root, or use Redfish over the network (easier) |

## 7. Clean up after the test

```bash
docker compose --profile node --profile node-hostnet down && docker image rm cpubench:latest
```
