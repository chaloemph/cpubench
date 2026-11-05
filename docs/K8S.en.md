# Testing on Kubernetes

[ภาษาไทย](K8S.md) · English

The question this test answers: **how does a web app perform in a pod, compared between the nodes of each machine (amd64 or arm64) and against the bare metal of the same machine?**

It uses the same hub and buttons as [INSTALL.en.md](INSTALL.en.md); the only difference is that the node runs as a pod instead of under systemd.

```
 hub (lg01:8090) ◄──── poll ──── pod "arm-01-k8s"   (arm64, 8 CPU, Guaranteed QoS)
        ▲         ◄──── poll ──── pod "x86-01-k8s"  (amd64, 8 CPU, Guaranteed QoS)
        │
 lg01 ── web test ──► <arm-01-ip>:30080 (NodePort) ──► pod :8080
```

- A pod joins the hub as `<k8s node name>-k8s` and is grouped as machine `<k8s node name>`
- If the bare metal of the same machine joined the hub under the k8s node's name (for example `arm-01`), the report shows a **"Virtualisation / container overhead"** section with how many % slower the pod is than native

> If the machines you compare are at different sites or clusters, see [INSTALL.en.md section 8.1](INSTALL.en.md#81-if-the-machines-you-compare-are-in-different-places-sites--clusters--networks) for how to merge results from several hubs into one report.

## 1. What you need

| Need | Details |
|---|---|
| Cluster | A node for every machine you compare (`kubernetes.io/arch` can be `amd64` or `arm64`; one mixed-arch cluster or several clusters both work) |
| Hub | Installed as in INSTALL.en.md step 3, and pods must reach `http://<lg01-ip>:8090` |
| Registry | One every node can pull the image from |
| Firewall | lg01 can reach the NodePorts (30080/30081) of the nodes under test |
| Tools | `helm` 3 (without Helm, use the files in `deploy/k8s/`) |

### 1.1 Configure kubelet on the nodes under test (important)

Without this, the pod is limited by a CFS quota but still runs across every core, so results swing and include throttling.
Set the **static CPU manager** so that a pod requesting whole CPUs gets cores of its own:

```yaml
# /var/lib/kubelet/config.yaml (add or change these fields)
cpuManagerPolicy: static
reservedSystemCPUs: "0-3"              # leave cores for the system and kubelet
topologyManagerPolicy: single-numa-node # keep a pod's CPUs in one NUMA node (very important on machines with several NUMA nodes)
topologyManagerScope: pod
```

Changing the policy must be done one node at a time, in this order:

```bash
kubectl drain arm-01 --ignore-daemonsets --delete-emptydir-data
```
```bash
sudo systemctl stop kubelet && sudo rm -f /var/lib/kubelet/cpu_manager_state && sudo systemctl start kubelet
```
```bash
kubectl uncordon arm-01
```

Do the same on x86-01 with the same number of `reservedSystemCPUs` on every machine.

## 2. Build and push a multi-arch image

```bash
make image-push IMAGE=registry.local/cpubench
```

It uses the `slim` (distroless) image, which cross-compiles without QEMU. Check that both architectures are there, once for each:

```bash
cpubench scan --arch amd64 --image registry.local/cpubench:latest
cpubench scan --arch arm64 --image registry.local/cpubench:latest
```

Both must show the status `ready`, and the platforms column must list `linux/amd64` and `linux/arm64`.

## 3. Create the namespace and secret

```bash
kubectl create namespace cpubench
```
```bash
kubectl -n cpubench create secret generic cpubench --from-literal=token='<hub token>' --from-literal=bmcPassword='<BMC password>'
```

If the BMC password differs per machine, create one secret per machine and add `--set tokenSecret=<secret name>` when you install.

## 4. Way A (recommended): the pod joins the hub and you press buttons

Install one release per machine under test, with different NodePorts:

```bash
helm -n cpubench install cb-arm deploy/helm/cpubench --set mode=node,arch=arm64,nodeName=arm-01,cpu=8,memory=8Gi,hubURL=http://<lg01-ip>:8090,image.repository=registry.local/cpubench,node.webNodePort=30080,node.power=redfish://Administrator@<arm-01-bmc-ip>
```
```bash
helm -n cpubench install cb-x86 deploy/helm/cpubench --set mode=node,arch=amd64,nodeName=x86-01,cpu=8,memory=8Gi,hubURL=http://<lg01-ip>:8090,image.repository=registry.local/cpubench,node.webNodePort=30081,node.power=redfish://admin@<x86-bmc-ip>
```

Check that the pods run and have joined the hub:

```bash
kubectl -n cpubench get pods -o wide && kubectl -n cpubench logs deploy/cpubench-node-arm64 | head -3
```

You must see the line `node "arm-01-k8s" ... connected to http://<lg01-ip>:8090`.

Open `http://<lg01-ip>:8090`. You will see `arm-01-k8s` and `x86-01-k8s` (the machine column shows `arm-01` / `x86-01`):

1. Press **smoke** on both pods to check everything works
2. Press **standard** on both pods
3. **Web test**: Server = `arm-01-k8s`, Load generator = `lg01`, then repeat with `x86-01-k8s`
4. Press **Compare latest of every machine**

If you already ran **standard** on the bare metal of `arm-01` and `x86-01` in the same round (as in INSTALL.en.md), the report shows both the **comparison between machines** and **pod vs native** for each machine.

**Pod memory:** set `memory` to at least **2–4 GiB**. With a lower limit, memory-heavy workloads (latency 512 MB, triad 384 MB) are skipped so the pod is not OOM-killed, and the report shows a `mem-limit` finding that lists what was skipped. The rest are still measured and comparable as usual.

**Pod size:**
- `cpu` is the size of the pod you want to simulate. Set it to the size you will really use in production (for example 4, 8 or 16)
- Use the same value on every machine so that the comparison is "per pod"
- To measure the whole machine, set `cpu` close to the total core count (minus `reservedSystemCPUs`)

## 5. Way B: a one-off Job (no buttons)

```bash
helm -n cpubench install run-arm deploy/helm/cpubench --set mode=auto,arch=arm64,nodeName=arm-01,cpu=8,memory=8Gi,auto.profile=standard,hubURL=http://<lg01-ip>:8090,image.repository=registry.local/cpubench
```
```bash
kubectl -n cpubench logs -f job/cpubench-auto-arm64
```

When it finishes, the result uploads to the hub by itself. For other machines do the same with that machine's `arch` and `nodeName` (for example `arch=amd64,nodeName=x86-01`) and a different release name.
Without Helm, edit `HUB_IP` and `REGISTRY` in [deploy/k8s/auto-job.yaml](../deploy/k8s/auto-job.yaml) or [deploy/k8s/node.yaml](../deploy/k8s/node.yaml) and choose the architecture with `ARCH`: `sed 's/ARCH/arm64/g' deploy/k8s/node.yaml | kubectl apply -n cpubench -f -`

## 6. Check the setup

```bash
kubectl -n cpubench exec deploy/cpubench-node-arm64 -- cpubench info
```

Look at the `Context` line:
- ✅ `k8s · effective CPUs 8 · allowed 12-19 ...` means the pod got its own cores from the static CPU manager
- ⚠️ `allowed 0-255 ... quota 8.00 cores` means it is still a CFS quota (the static policy is not set, or the pod is not Guaranteed QoS). Results include throttling and the report shows `cgroup-throttled`

```bash
kubectl -n cpubench get pod -l app=cpubench-node -o jsonpath='{range .items[*]}{.metadata.name}{"  "}{.status.qosClass}{"\n"}{end}'
```

Every pod must be `Guaranteed`.

## 7. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| A job shows `failed: node lost the job` | The pod restarted during the run, usually from OOM (see `Last State: OOMKilled` in `kubectl describe pod`). Raise `memory` |
| The pod stays `Pending` | No node matches `arch`/`nodeName`, or not enough CPU (`cpu` + `reservedSystemCPUs` exceeds the core count) |
| `exec format error` / `CrashLoopBackOff` right away | The image has no such architecture. Push again with `make image-push` and check with `cpubench scan --arch <amd64\|arm64> --image` |
| The pod does not appear in the hub | `kubectl logs` → `401` means the token in the secret is wrong; `connection refused` means the pod cannot reach the hub (check NetworkPolicy, egress, firewall) |
| The web test fails at the first step | lg01 cannot reach `http://<node-ip>:30080/healthz`. Check the node's firewall and use a different NodePort for each release |
| `power: ... timeout` | The pod cannot reach the BMC IP (the BMC is often on another VLAN). Open a route, or leave `node.power` unset and read power from the bare-metal node instead |
| The pod is far slower than native | Look for `cgroup-throttled`. Check the static CPU manager, the topology manager and the CNI (some CNIs use a lot of CPU in the web test) |

## 8. Clean up after the test

```bash
helm -n cpubench uninstall cb-arm cb-x86 && kubectl delete namespace cpubench
```

If you changed kubelet only for this test, restore it with the same steps as section 1.1.
