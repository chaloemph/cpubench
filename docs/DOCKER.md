# ทดสอบบน Docker

คำถามที่การทดสอบนี้ตอบ: **ถ้ารัน web app ใน container จะได้ประสิทธิภาพเท่าไหร่ เทียบระหว่างเครื่องที่จะเลือก (x86-64 หรือ ARM64 จากผู้ขายใดก็ได้) และเทียบกับ bare metal ของเครื่องเดียวกัน** รวมถึง overhead ของ network แบบ bridge (docker-proxy/NAT) เทียบกับ `--network host`

ใช้ hub และปุ่มชุดเดียวกับ [INSTALL.md](INSTALL.md) ต่างกันแค่ node ไปรันใน container

```
 hub (lg01:8090) ◄── poll ── container "arm-01-docker"        (bridge, -p 8080, 8 CPU, 8 GiB)
                 ◄── poll ── container "arm-01-docker-hostnet" (network host)
 lg01 ── web test ──► <arm-01-ip>:8080 ──► docker-proxy ──► container :8080
```

ทุก container ใส่ `--machine arm-01` ไว้ ถ้าเคยรัน bare metal ของ `arm-01` ด้วย report จะแสดง **Docker vs native** ให้อัตโนมัติ

> ถ้าเครื่องที่จะเทียบอยู่คนละ site หรือ cluster ให้ดู [INSTALL.md ข้อ 8.1](INSTALL.md#81-ถ้าเครื่องที่จะเทียบอยู่คนละที่-คนละ-site--cluster--network) วิธีรวมผลจากหลาย hub ไว้ใน report เดียว

## 1. สิ่งที่ต้องมี

- Docker Engine + compose plugin บน SUT ทุกเครื่อง (**เวอร์ชันเดียวกัน**)
- hub ที่ติดตั้งตาม INSTALL.md ขั้นที่ 3 และ container ต้องเข้าถึง `http://<lg01-ip>:8090` ได้
  - **ห้ามใช้ `localhost`** เพราะใน container จะหมายถึงตัว container เอง
- ไฟล์ `deploy/docker/compose.yaml` บนแต่ละเครื่อง

## 2. นำ image ไปไว้บนเครื่อง

**แบบที่ 1: มี registry**

```bash
make image-push IMAGE=registry.local/cpubench
```

ทุกเครื่องใช้ `CPUBENCH_IMAGE=registry.local/cpubench:latest` แล้ว Docker จะ pull arch ที่ตรงกับเครื่องให้เอง

**แบบที่ 2: ไม่มี registry** (build image ของทั้งสอง arch บนเครื่องใดก็ได้ โดยไม่ต้องใช้ QEMU)

```bash
make image-tar
```
```bash
scp dist/cpubench-image-arm64.tar deploy/docker/compose.yaml ubuntu@arm-01:~/
```
```bash
ssh ubuntu@arm-01 'docker load -i cpubench-image-arm64.tar && docker tag cpubench:latest-arm64 cpubench:latest && docker image inspect cpubench:latest --format "{{.Architecture}}"'
```

ต้องได้ผลเป็น `arm64` ส่วนเครื่อง amd64 ทำแบบเดียวกันด้วยไฟล์ `cpubench-image-amd64.tar` และ tag `cpubench:latest-amd64`

## 3. วิธี A (แนะนำ): container เข้า hub แล้วกดปุ่ม

บน **arm-01** (ในโฟลเดอร์ที่มี `compose.yaml`):

```bash
export HUB=http://<lg01-ip>:8090 CPUBENCH_TOKEN='<hub token>' MACHINE=arm-01 HOST_IP=<arm-01-ip> CPUS=8 MEMORY=8g POWER=redfish://<bmc-user>@<bmc-ip> CPUBENCH_BMC_PASSWORD='<BMC password>'
```
```bash
docker compose --profile node up -d && docker compose --profile node logs --tail 5
```

ต้องเห็นบรรทัด `node "arm-01-docker" ... connected to http://<lg01-ip>:8090 · web tests reach it at <arm-01-ip>:8080`

ถ้าต้องการวัด overhead ของ network แบบ bridge ด้วย ให้เปิด container แบบ host network เพิ่ม:

```bash
WEB_PORT=8081 docker compose --profile node-hostnet up -d
```

ทำซ้ำบน **x86-01** ด้วย `MACHINE=x86-01`, `HOST_IP` ของเครื่องนั้น, BMC ของเครื่องนั้น และ **CPUS / MEMORY เท่ากับเครื่องแรก**

| ตัวแปร | ความหมาย |
|---|---|
| `HUB` | URL ของ hub (ต้องเป็น IP ที่ container เข้าถึงได้) |
| `MACHINE` | ชื่อเครื่องจริง ใช้จับกลุ่มกับผล bare metal |
| `HOST_IP` | IP ของเครื่องที่ lg01 ใช้ยิง web test (IP ภายใน container 172.x เข้าถึงจากข้างนอกไม่ได้) |
| `CPUS`, `MEMORY` | ขนาด container ตั้งให้เท่ากับที่จะใช้จริงใน production และเท่ากันทุกเครื่อง |
| `POWER` | แหล่งค่าไฟ (Redfish ผ่าน network ใช้จาก container ได้) |
| `WEB_PORT` | port ที่ publish ให้ web test (ค่าเริ่มต้น 8080) |
| `NODE_NAME` | ชื่อบน hub (ค่าเริ่มต้น `<MACHINE>-docker`) |

จากนั้นเปิด `http://<lg01-ip>:8090`:

1. กด **smoke** ที่ `arm-01-docker` และ `x86-01-docker` เพื่อเช็คว่าทุกอย่างทำงาน
2. กด **standard** ทั้งสองตัว
3. **Web test**: Server = `arm-01-docker`, Load generator = `lg01` แล้วทำซ้ำกับ `x86-01-docker` (และ `-docker-hostnet` ถ้าเปิดไว้)
4. กด **Compare latest of every machine** (ใส่ Baseline เป็นเครื่องที่ใช้งานจริง) จะได้ report ทั้ง **เทียบระหว่างเครื่อง** และ **Docker / Docker-hostnet vs native**

### Pin CPU ให้อยู่ใน NUMA node เดียว (แนะนำบนเครื่องที่มีหลาย NUMA node)

`CPUS=8` คือการจำกัดด้วย quota แต่ thread ยังวิ่งได้ทุก core ถ้าต้องการให้เหมือน pod ที่ได้ core เฉพาะ ให้เปิด 2 บรรทัดนี้ใน `x-limits` ของ `compose.yaml`:

```yaml
  cpuset: "0-7"     # CPU ของ NUMA node 0 ดูได้จาก: cpubench info หรือ numactl -H
  cpuset_mems: "0"
```

## 4. วิธี B: ไม่ใช้ hub (คำสั่งเดียว)

```bash
docker run --rm --cpus 8 --memory 8g --user root -v "$PWD/results:/work/results" cpubench:latest auto --profile standard --label arm-01-docker --machine arm-01
```

ได้ไฟล์ผลใน `./results/` จากนั้นทำ web test แยก:

```bash
docker run -d --name cpubench-sut --cpus 8 --memory 8g -p 8080:8080 cpubench:latest web serve        # บน arm-01
```
```bash
cpubench web load --target http://<arm-01-ip>:8080 --label arm-01-docker --machine arm-01 --adaptive   # บน lg01
```
```bash
docker rm -f cpubench-sut
```

รวมผลทั้งหมดด้วย:

```bash
cpubench compare --latest results/
```

## 5. ตรวจว่าการตั้งค่าถูกต้อง

```bash
docker compose --profile node exec node cpubench info
```

บรรทัด `Context` ต้องเป็น `container · effective CPUs 8 ... quota 8.00 cores` ซึ่งเป็นไปตาม `CPUS` (ถ้าใช้ cpuset จะเห็น `allowed 0-7`)

finding ที่จะเจอใน report และความหมาย:

| finding | ความหมาย |
|---|---|
| `cgroup-limit` | เป็นข้อมูลแจ้งเฉยๆ ว่า container ถูกจำกัด CPU ไว้เท่าไหร่ |
| `cgroup-throttled` | โดน quota throttle ระหว่างวัด ถ้าเยอะให้ใช้ `cpuset` แทนการใช้แค่ `CPUS` |
| `mem-limit` | ข้าม workload ที่ใหญ่เกิน memory ของ container ให้เพิ่ม `MEMORY` (2–4 GiB ขึ้นไป) ถ้าต้องการวัดให้ครบ |

## 6. แก้ปัญหา

| อาการ | สาเหตุ / วิธีแก้ |
|---|---|
| node ไม่ขึ้นใน hub | `HUB` เป็น `localhost` หรือ IP ที่ container เข้าไม่ถึง ให้ใช้ IP จริงของ lg01 และตรวจ token |
| warning `running in a container; web tests will dial 172.x` | ลืมตั้ง `HOST_IP` |
| web test fail ตั้งแต่ step แรก | lg01 เข้าถึง `http://<HOST_IP>:<WEB_PORT>/healthz` ไม่ได้ ให้ตรวจ firewall และตรวจว่า port ถูก publish แล้ว |
| `exec format error` | image ผิด arch ตรวจด้วย `docker image inspect cpubench:latest --format '{{.Architecture}}'` |
| job ขึ้น `node lost the job` | container restart ระหว่างรัน ดูด้วย `docker inspect <container> --format '{{.State.OOMKilled}}'` ถ้าเป็น true ให้เพิ่ม `MEMORY` |
| `permission denied` ตอนเขียน `./results` | image แบบ slim รันเป็น user ที่ไม่ใช่ root ถ้า mount โฟลเดอร์จาก host ต้องเพิ่ม `--user root` (compose ตั้งไว้ให้แล้ว) |
| อ่านค่าไฟด้วย `ipmi://local` ไม่ได้ | ต้องใช้ image `full` + `--device /dev/ipmi0` + root หรือใช้ Redfish ผ่าน network แทน (ง่ายกว่า) |

## 7. ลบออกหลังทดสอบ

```bash
docker compose --profile node --profile node-hostnet down && docker image rm cpubench:latest
```
