# ทดสอบบน Kubernetes

ภาษาไทย · [English](K8S.en.md)

คำถามที่การทดสอบนี้ตอบ: **ถ้ารัน web app ใน pod จะได้ประสิทธิภาพเท่าไหร่ เทียบระหว่าง node ของแต่ละเครื่อง (amd64 หรือ arm64) และเทียบกับ bare metal ของเครื่องเดียวกัน**

ใช้ hub และปุ่มชุดเดียวกับ [INSTALL.md](INSTALL.md) ต่างกันแค่ node ไปรันเป็น pod แทน systemd

```
 hub (lg01:8090) ◄──── poll ──── pod "arm-01-k8s"   (arm64, 8 CPU, Guaranteed QoS)
        ▲         ◄──── poll ──── pod "x86-01-k8s"  (amd64, 8 CPU, Guaranteed QoS)
        │
 lg01 ── web test ──► <arm-01-ip>:30080 (NodePort) ──► pod :8080
```

- pod เข้า hub ด้วยชื่อ `<ชื่อ k8s node>-k8s` และถูกจัดกลุ่มเป็น machine `<ชื่อ k8s node>`
- ถ้า bare metal ของเครื่องเดียวกันเข้า hub ด้วยชื่อเดียวกับชื่อ k8s node (เช่น `arm-01`) report จะแสดงส่วน **"Virtualisation / container overhead"** ว่า pod ช้ากว่า native กี่ %

> ถ้าเครื่องที่จะเทียบอยู่คนละ site หรือ cluster ให้ดู [INSTALL.md ข้อ 8.1](INSTALL.md#81-ถ้าเครื่องที่จะเทียบอยู่คนละที่-คนละ-site--cluster--network) วิธีรวมผลจากหลาย hub ไว้ใน report เดียว

## 1. สิ่งที่ต้องมี

| สิ่งที่ต้องมี | รายละเอียด |
|---|---|
| Cluster | มี node ของทุกเครื่องที่จะเทียบ (`kubernetes.io/arch` เป็น `amd64` หรือ `arm64` ก็ได้ cluster เดียวแบบ mixed-arch หรือแยกหลาย cluster ก็ได้) |
| Hub | ติดตั้งตาม INSTALL.md ขั้นที่ 3 และ pod ต้องเข้าถึง `http://<lg01-ip>:8090` ได้ |
| Registry | ที่ทุก node pull image ได้ |
| Firewall | lg01 เข้าถึง NodePort (30080/30081) ของ node ที่จะทดสอบได้ |
| เครื่องมือ | `helm` 3 (ถ้าไม่มี Helm ให้ใช้ไฟล์ใน `deploy/k8s/`) |

### 1.1 ตั้ง kubelet บน node ที่จะทดสอบ (สำคัญ)

ถ้าไม่ตั้งค่านี้ pod จะถูกจำกัดด้วย CFS quota แต่ยังวิ่งข้ามทุก core ผลจะแกว่งและมี throttle ปน
ให้ตั้ง **static CPU manager** เพื่อให้ pod ที่ขอ CPU เป็นจำนวนเต็มได้ core เฉพาะของตัวเอง:

```yaml
# /var/lib/kubelet/config.yaml (เพิ่มหรือแก้ field เหล่านี้)
cpuManagerPolicy: static
reservedSystemCPUs: "0-3"              # เว้น core ไว้ให้ระบบและ kubelet
topologyManagerPolicy: single-numa-node # ให้ CPU ของ pod อยู่ใน NUMA node เดียว (สำคัญมากบนเครื่องที่มีหลาย NUMA node)
topologyManagerScope: pod
```

การเปลี่ยน policy ต้องทำทีละ node ตามลำดับนี้:

```bash
kubectl drain arm-01 --ignore-daemonsets --delete-emptydir-data
```
```bash
sudo systemctl stop kubelet && sudo rm -f /var/lib/kubelet/cpu_manager_state && sudo systemctl start kubelet
```
```bash
kubectl uncordon arm-01
```

ทำแบบเดียวกันบน x86-01 โดยใช้ `reservedSystemCPUs` จำนวนเท่ากันทุกเครื่อง

## 2. Build และ push image แบบ multi-arch

```bash
make image-push IMAGE=registry.local/cpubench
```

ใช้ image แบบ `slim` (distroless) ซึ่ง cross-compile ได้โดยไม่ต้องใช้ QEMU ตรวจว่ามีทั้งสอง arch:

```bash
cpubench scan --arch amd64 --image registry.local/cpubench:latest
cpubench scan --arch arm64 --image registry.local/cpubench:latest
```

ทั้งสองคำสั่งต้องได้สถานะ `ready` และคอลัมน์ platforms ต้องมี `linux/amd64` และ `linux/arm64`

## 3. สร้าง namespace และ secret

```bash
kubectl create namespace cpubench
```
```bash
kubectl -n cpubench create secret generic cpubench --from-literal=token='<hub token>' --from-literal=bmcPassword='<BMC password>'
```

ถ้ารหัสผ่าน BMC ของแต่ละเครื่องไม่เหมือนกัน ให้สร้าง secret แยกต่อเครื่อง และใส่ `--set tokenSecret=<ชื่อ secret>` ตอน install

## 4. วิธี A (แนะนำ): pod เข้า hub แล้วกดปุ่ม

install หนึ่ง release ต่อเครื่องที่จะทดสอบ และใช้ NodePort ต่างกัน:

```bash
helm -n cpubench install cb-arm deploy/helm/cpubench --set mode=node,arch=arm64,nodeName=arm-01,cpu=8,memory=8Gi,hubURL=http://<lg01-ip>:8090,image.repository=registry.local/cpubench,node.webNodePort=30080,node.power=redfish://Administrator@<arm-01-bmc-ip>
```
```bash
helm -n cpubench install cb-x86 deploy/helm/cpubench --set mode=node,arch=amd64,nodeName=x86-01,cpu=8,memory=8Gi,hubURL=http://<lg01-ip>:8090,image.repository=registry.local/cpubench,node.webNodePort=30081,node.power=redfish://admin@<x86-bmc-ip>
```

ตรวจว่า pod รันและเชื่อมต่อ hub แล้ว:

```bash
kubectl -n cpubench get pods -o wide && kubectl -n cpubench logs deploy/cpubench-node-arm64 | head -3
```

ต้องเห็นบรรทัด `node "arm-01-k8s" ... connected to http://<lg01-ip>:8090`

เปิด `http://<lg01-ip>:8090` แล้วจะเห็น `arm-01-k8s` และ `x86-01-k8s` (คอลัมน์ machine เป็น `arm-01` / `x86-01`):

1. กด **smoke** ที่ทั้งสอง pod เพื่อเช็คว่าทุกอย่างทำงาน
2. กด **standard** ที่ทั้งสอง pod
3. **Web test**: Server = `arm-01-k8s`, Load generator = `lg01` แล้วทำซ้ำกับ `x86-01-k8s`
4. กด **Compare latest of every machine**

ถ้าในรอบเดียวกันเคยรัน **standard** บน bare metal ของ `arm-01` และ `x86-01` ไว้แล้ว (ตาม INSTALL.md) report จะแสดงทั้ง **การเทียบระหว่างเครื่อง** และ **pod vs native** ของแต่ละเครื่องพร้อมกัน

**Memory ของ pod:** ตั้ง `memory` อย่างน้อย **2–4 GiB** ถ้า limit ต่ำกว่านั้น workload ที่ใช้ memory มาก (latency 512 MB, triad 384 MB) จะถูกข้ามไปเพื่อไม่ให้ pod โดน OOM kill และ report จะแสดง finding `mem-limit` ว่าข้ามอะไรไปบ้าง ส่วนที่เหลือยังวัดและเทียบกันได้ตามปกติ

**ขนาด pod:**
- `cpu` คือขนาดของ pod ที่ต้องการจำลอง ให้ตั้งเท่ากับขนาดที่จะใช้จริงใน production (เช่น 4, 8 หรือ 16)
- ใช้ค่าเดียวกันทุกเครื่อง เพื่อให้เทียบกัน "ต่อ pod" ได้
- ถ้าต้องการวัดแบบเต็มเครื่อง ให้ตั้ง `cpu` เกือบเท่าจำนวน core ทั้งหมด (หัก `reservedSystemCPUs` ออก)

## 5. วิธี B: Job ครั้งเดียว (ไม่ต้องกดปุ่ม)

```bash
helm -n cpubench install run-arm deploy/helm/cpubench --set mode=auto,arch=arm64,nodeName=arm-01,cpu=8,memory=8Gi,auto.profile=standard,hubURL=http://<lg01-ip>:8090,image.repository=registry.local/cpubench
```
```bash
kubectl -n cpubench logs -f job/cpubench-auto-arm64
```

เมื่อจบ ผลจะ upload ขึ้น hub เอง ส่วนเครื่องอื่นทำแบบเดียวกันด้วย `arch` และ `nodeName` ของเครื่องนั้น (เช่น `arch=amd64,nodeName=x86-01`) และชื่อ release อื่น
ถ้าไม่มี Helm ให้แก้ `HUB_IP` และ `REGISTRY` ในไฟล์ [deploy/k8s/auto-job.yaml](../deploy/k8s/auto-job.yaml) หรือ [deploy/k8s/node.yaml](../deploy/k8s/node.yaml) แล้วเลือกสถาปัตยกรรมด้วย `ARCH`: `sed 's/ARCH/arm64/g' deploy/k8s/node.yaml | kubectl apply -n cpubench -f -`

## 6. ตรวจว่าการตั้งค่าถูกต้อง

```bash
kubectl -n cpubench exec deploy/cpubench-node-arm64 -- cpubench info
```

ดูบรรทัด `Context`:
- ✅ `k8s · effective CPUs 8 · allowed 12-19 ...` แปลว่าได้ core เฉพาะของตัวเองจาก static CPU manager
- ⚠️ `allowed 0-255 ... quota 8.00 cores` แปลว่ายังเป็น CFS quota (ยังไม่ได้ตั้ง static policy หรือ pod ไม่ใช่ Guaranteed QoS) ผลจะมี throttle ปน และ report จะแสดง `cgroup-throttled`

```bash
kubectl -n cpubench get pod -l app=cpubench-node -o jsonpath='{range .items[*]}{.metadata.name}{"  "}{.status.qosClass}{"\n"}{end}'
```

ทุก pod ต้องเป็น `Guaranteed`

## 7. แก้ปัญหา

| อาการ | สาเหตุ / วิธีแก้ |
|---|---|
| job ขึ้น `failed: node lost the job` | pod restart ระหว่างรัน มักเกิดจาก OOM (ดูด้วย `kubectl describe pod` ที่ `Last State: OOMKilled`) ให้เพิ่ม `memory` |
| pod ค้างที่ `Pending` | ไม่มี node ที่ตรง `arch`/`nodeName` หรือ CPU ไม่พอ (`cpu` + `reservedSystemCPUs` เกินจำนวน core) |
| `exec format error` / `CrashLoopBackOff` ทันที | image ไม่มี arch นั้น ให้ push ใหม่ด้วย `make image-push` และตรวจด้วย `cpubench scan --arch <amd64\|arm64> --image` |
| pod ไม่ขึ้นใน hub | `kubectl logs` → ถ้า `401` แปลว่า token ใน secret ผิด ถ้า `connection refused` แปลว่า pod เข้าถึง hub ไม่ได้ (ตรวจ NetworkPolicy, egress, firewall) |
| web test fail ตั้งแต่ step แรก | lg01 เข้าถึง `http://<node-ip>:30080/healthz` ไม่ได้ ให้ตรวจ firewall ของ node และใช้ NodePort ไม่ซ้ำกันในแต่ละ release |
| `power: ... timeout` | pod เข้าถึง IP ของ BMC ไม่ได้ (BMC มักอยู่คนละ VLAN) ให้เปิด route หรือเว้น `node.power` ไว้แล้วอ่านค่าไฟจาก node ของ bare metal แทน |
| ผล pod ช้ากว่า native มากผิดปกติ | ดูว่ามี `cgroup-throttled` หรือไม่ ตรวจ static CPU manager, topology manager และ CNI (บาง CNI ใช้ CPU เยอะใน web test) |

## 8. ลบออกหลังทดสอบ

```bash
helm -n cpubench uninstall cb-arm cb-x86 && kubectl delete namespace cpubench
```

ถ้าเปลี่ยน kubelet ไว้เฉพาะเพื่อการทดสอบ ให้คืนค่าเดิมด้วยขั้นตอนเดียวกับข้อ 1.1
