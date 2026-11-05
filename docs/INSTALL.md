# ติดตั้ง cpubench hub + node (x86-64 และ ARM64)

เป้าหมาย: ติดตั้งครั้งเดียว จากนั้นทุกการทดสอบสั่งด้วยปุ่มบนหน้าเว็บ hub
ใช้เวลาติดตั้งประมาณ 30–45 นาที (ส่วนใหญ่เป็นการตั้งค่า BIOS และ BMC)

## 0. วางเครื่อง

```
                     ┌──────────────────────────────┐
                     │ lg01  (load generator + hub)  │  x86 เครื่องที่ 3, ≥16 core, NIC ≥10GbE
                     │  cpubench hub    :8090        │
                     │  cpubench node   (lg01)       │
                     └──────────────┬───────────────┘
              switch เดียวกัน        │
          ┌─────────────────────────┴─────────────────────────┐
┌─────────┴──────────────┐                        ┌───────────┴────────────┐
│ arm-01 (ARM, SUT)   │                        │ x86-01 (x86, SUT)  │
│  cpubench node :8080   │                        │  cpubench node :8080   │
│  BMC   → Redfish/IPMI  │                        │  BMC   → Redfish/IPMI  │
└────────────────────────┘                        └────────────────────────┘
```

| บทบาท | เครื่อง | ทำไม |
|---|---|---|
| **SUT** (server ที่จะวัด) | ทุกเครื่องที่จะเทียบ (ในคู่มือนี้คือ `arm-01` และ `x86-01` ใช้ได้กับทุกผู้ขาย และจะเป็น x86 ทั้งคู่ หรือ ARM ทั้งคู่ก็ได้) | ต้องใช้ Ubuntu เวอร์ชันเดียวกัน kernel ใกล้เคียงกัน และตั้ง BIOS แบบเดียวกัน |
| **Load generator** | เครื่องที่ 3 (แนะนำ) | ถ้าใช้ SUT ยิงกันเอง เครื่องที่ยิงจะแย่ง CPU กับงานที่วัด ผลจะเพี้ยน |
| **Hub** | อยู่เครื่องเดียวกับ load generator ได้ | ใช้ทรัพยากรน้อยมาก และใช้ port 8090 จึงไม่ชนกับ `web serve` ที่ใช้ 8080 |

ถ้ามีแค่ 2 เครื่อง ยังทดสอบ micro, stress และ power ได้ครบ ส่วน web test ให้ใช้เครื่องหนึ่งยิงอีกเครื่อง แล้วสลับกัน และให้ถือว่าผล web เป็นค่าประมาณ

**Port ที่ต้องเปิดในเครือข่ายทดสอบ:**

| ต้นทาง → ปลายทาง | port | ใช้ทำอะไร |
|---|---|---|
| ทุก node → hub | 8090/tcp | poll หา job และ upload ผล |
| browser ของคุณ → hub | 8090/tcp | หน้าเว็บ hub |
| lg01 → SUT | 8080/tcp | web test |
| SUT → BMC ของตัวเอง | 443/tcp (Redfish) หรือ 623/udp (IPMI) | อ่านค่าไฟ |

## 1. เตรียมทุกเครื่อง (SUT ทุกเครื่อง และ lg01)

### 1.1 OS และ package

```bash
sudo apt update && sudo apt install -y numactl dmidecode ipmitool ethtool chrony linux-tools-common linux-tools-$(uname -r)
```

- ถ้าใช้ Ubuntu 22.04 ให้ติดตั้ง HWE kernel ด้วย เพื่อให้ได้ kernel 6.8 เหมือน 24.04 แล้ว reboot: `sudo apt install -y linux-generic-hwe-22.04`
- `chrony` ใช้ sync เวลาให้ตรงกัน จำเป็นเมื่อยิงโหลดจากหลายเครื่อง

### 1.2 BIOS / BMC (ทำบน SUT ทุกเครื่องให้เหมือนกัน)

| ตั้งค่า | ค่าที่ใช้ |
|---|---|
| Power policy | **Performance** หรือ OS-controlled (ไม่ใช่ Energy saving) |
| SMT / Hyper-Threading | ตั้งเหมือนกันทุกเครื่อง (CPU บางรุ่นไม่มี SMT ให้บันทึกไว้ในผล) |
| Memory | ใส่ DIMM ครบทุก channel และเท่ากันทุก socket |
| Redfish | เปิด และมี user ที่อ่านค่า Power ได้ (iDRAC, iLO, XClarity, iBMC, OpenBMC ฯลฯ) |
| IPMI over LAN | เปิด ถ้าจะใช้ `ipmi://` แทน Redfish |

### 1.3 CPU governor (ทำบน SUT)

```bash
sudo cpupower frequency-set -g performance
```

## 2. ติดตั้ง binary

build บนเครื่องพัฒนา (ที่มี repo นี้):

```bash
make build-all
```

ได้ไฟล์ `dist/cpubench-linux-amd64`, `dist/cpubench-linux-arm64` และ `dist/SHA256SUMS` แล้ว copy ไปทุกเครื่อง:

```bash
scp dist/cpubench-linux-arm64 dist/SHA256SUMS deploy/systemd/*.service scripts/collect-info.sh ubuntu@arm-01:/tmp/
```
```bash
scp dist/cpubench-linux-amd64 dist/SHA256SUMS deploy/systemd/*.service scripts/collect-info.sh ubuntu@x86-01:/tmp/
```
```bash
scp dist/cpubench-linux-amd64 dist/SHA256SUMS deploy/systemd/*.service ubuntu@lg01:/tmp/
```

คำสั่งในขั้นต่อไปที่อ้างถึง `deploy/systemd/...` และ `scripts/...` ให้ใช้ไฟล์ใน `/tmp/` แทน

บนแต่ละเครื่อง ตรวจ checksum แล้วติดตั้ง (คำสั่งเลือก arch ให้อัตโนมัติ):

```bash
cd /tmp && sha256sum --ignore-missing -c SHA256SUMS && sudo install -m755 cpubench-linux-$(dpkg --print-architecture) /usr/local/bin/cpubench && cpubench version
```

## 3. ติดตั้ง hub (บน lg01)

```bash
echo "CPUBENCH_TOKEN=$(openssl rand -hex 16)" | sudo tee /etc/cpubench-hub.env >/dev/null && sudo chmod 600 /etc/cpubench-hub.env && sudo cat /etc/cpubench-hub.env
```

**จด token ไว้** ต้องใช้ตอนตั้งค่าทุก node และเป็น password ตอนกดปุ่มบนหน้าเว็บ

```bash
sudo cp deploy/systemd/cpubench-hub.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now cpubench-hub && systemctl status cpubench-hub --no-pager
```

(ถ้าบน lg01 ไม่มี repo ให้ copy ไฟล์ `deploy/systemd/*.service` ไปด้วยตอนขั้นที่ 2)

ทดสอบด้วยการเปิด `http://<lg01-ip>:8090` ใน browser ควรเห็นหน้า "cpubench hub" ที่ยังไม่มีเครื่องเชื่อมต่อ

## 4. ติดตั้ง node บน SUT เครื่องแรก (`arm-01`)

### 4.1 ตรวจเครื่องก่อน

```bash
sudo cpubench doctor
```

แก้รายการที่เป็น `warn` ก่อน (governor, DIMM, NUMA/NIC) และเก็บข้อมูลเครื่องไว้ให้ทีม:

```bash
sudo bash scripts/collect-info.sh
```

### 4.2 ทดสอบว่าอ่านค่าไฟจาก BMC ได้

```bash
export CPUBENCH_BMC_PASSWORD='<BMC password>'
```
```bash
cpubench power --power redfish://<bmc-user>@<bmc-ip>
```

ควรได้ผลประมาณ `redfish https://<bmc-ip>: 245.0 W` ถ้าไม่ได้ ลองใช้ IPMI แทน (ต้องเปิด IPMI over LAN ใน BMC ก่อน):

```bash
cpubench power --power ipmi://<bmc-user>@<bmc-ip>
```

หรือใช้ in-band IPMI จากตัวเครื่องเอง (ไม่ต้องใช้ IP ของ BMC):

```bash
sudo cpubench power --power ipmi://local
```

### 4.3 ตั้งค่าและเปิด service

```bash
printf 'CPUBENCH_TOKEN=%s\nCPUBENCH_BMC_PASSWORD=%s\n' '<token จากขั้นที่ 3>' '<BMC password>' | sudo tee /etc/cpubench.env >/dev/null && sudo chmod 600 /etc/cpubench.env
```

```bash
sudo cp deploy/systemd/cpubench-node.service /etc/systemd/system/ && sudo systemctl edit --full cpubench-node
```

แก้บรรทัด `ExecStart` เป็น:

```
ExecStart=/usr/local/bin/cpubench node --hub http://<lg01-ip>:8090 --name arm-01 --power redfish://<bmc-user>@<bmc-ip>
```

```bash
sudo systemctl enable --now cpubench-node && journalctl -u cpubench-node -n 20 --no-pager
```

ใน log ควรเห็นบรรทัด `node "arm-01" (arm64, ...) connected to http://<lg01-ip>:8090`

## 5. ติดตั้ง node บน SUT เครื่องอื่น (`x86-01` ฯลฯ)

ทำเหมือนขั้นที่ 4 ทุกอย่าง ต่างกันแค่:
- ใช้ `--name x86-01`
- `--power` ชี้ไปที่ BMC ของเครื่องนั้น

> ⚠️ **วัดไฟด้วยวิธีเดียวกันทุกเครื่อง** ใช้ BMC (วัดทั้งเครื่อง) ทุกเครื่อง อย่าผสมกับ `rapl` (มีเฉพาะ x86 และวัดแค่ CPU package) เพราะเครื่องที่ใช้ rapl จะดูประหยัดไฟกว่าความจริง

## 6. ติดตั้ง node บน load generator (lg01)

load generator ไม่ต้องวัดไฟ จึงไม่ต้องใส่ `--power` แต่ต้องปรับ network ให้รองรับ connection จำนวนมาก:

```bash
printf 'net.core.somaxconn=65535\nnet.ipv4.ip_local_port_range=1024 65535\nnet.ipv4.tcp_tw_reuse=1\n' | sudo tee /etc/sysctl.d/90-cpubench.conf && sudo sysctl --system
```

ทำบน SUT ทุกเครื่องด้วย จากนั้นตั้ง service:

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

## 7. ทดสอบระบบ (smoke ใช้เวลาประมาณ 5 นาที)

เปิด `http://<lg01-ip>:8090`:

1. ตาราง **Machines** ต้องมี `arm-01`, `x86-01` และ `lg01` ขึ้นสถานะ **online** และ arm-01 กับ x86-01 ต้องมี power = yes
2. เลือก **smoke** แล้วกด **Run on every online machine** browser จะถาม password ให้ใส่ username อะไรก็ได้ และ password คือ token
3. รอจน job ทั้งหมดเป็น `done` (หน้าจะรีเฟรชเองทุก 5 วินาที)
4. ในส่วน **Web test** เลือก Server = `arm-01`, Load generator = `lg01`, profile = **smoke** แล้วกด **Run web test**
5. กด **Compare latest of every machine** ควรเห็น report ที่มีครบทุกส่วน

ถ้าผ่านทั้ง 5 ข้อ แปลว่าการติดตั้งถูกต้อง

## 8. ทดสอบจริง

| ลำดับ | กดปุ่ม | เวลาโดยประมาณ |
|---|---|---|
| 1 | **standard** ที่ arm-01 และ x86-01 (รันพร้อมกันได้เพราะเป็นคนละเครื่อง) | ~25 นาที |
| 2 | **Web test** server = arm-01, load generator = lg01, profile = **standard** | 10–20 นาที |
| 3 | **Web test** server = x86-01, load generator = lg01, profile = **standard** | 10–20 นาที |
| 4 | **Compare latest of every machine** | ได้ report ทันที |

ถ้าต้องการตาราง TCO ให้ดึงผลจาก hub แล้วใช้ CLI สร้าง report:

```bash
mkdir -p hub-results && curl -s http://<lg01-ip>:8090/api/results | python3 -c 'import json,sys;[print(r["id"]) for r in json.load(sys.stdin)]' | while read id; do curl -s -o hub-results/$id.json http://<lg01-ip>:8090/api/results/$id; done
```
```bash
cpubench compare --latest --baseline <เครื่องที่ใช้งานจริง> --tco examples/tco.yaml -o report.html --md report.md hub-results/
```

(แก้ราคาใน `examples/tco.yaml` และใช้ key เป็น `arm-01` / `x86-01` ให้ตรงกับชื่อ node)

## 8.1 ถ้าเครื่องที่จะเทียบอยู่คนละที่ (คนละ site / cluster / network)

ตารางเปรียบเทียบต้องการแค่**ไฟล์ผล JSON ของทุกเครื่อง** ไม่จำเป็นต้องอยู่ใน hub หรือ network เดียวกัน เลือกได้ 3 แบบ:

| แบบ | ใช้เมื่อ | วิธี |
|---|---|---|
| **A. hub เดียว** | ทุกที่เข้าถึง hub ได้ (node ต้องการแค่ HTTP ขาออกไปที่ port 8090) | ติดตั้งตามขั้นที่ 3–7 ตามปกติ |
| **B. hub แยกต่อที่** | network ไม่เชื่อมกัน แต่เครื่องของคุณเข้าถึงทั้งสอง hub ได้ | ติดตั้ง hub + load generator ที่ละชุด แล้วรวมผลด้วยคำสั่งเดียว (ด้านล่าง) |
| **C. ไม่มี network เชื่อมกันเลย** | อยู่ใน network ที่ถูกแยกขาด | copy ไฟล์ JSON ออกมา (`/var/lib/cpubench/results/` หรือ `/var/lib/cpubench-hub/`) แล้วรัน `compare` บนเครื่องไหนก็ได้ |

แบบ B: ดึงผลจาก hub ทั้งสองแล้วสร้าง report:

```bash
cpubench compare --latest --baseline <เครื่องที่ใช้งานจริง> --tco examples/tco.yaml -o report.html --md report.md http://<hub-site-a>:8090 http://<hub-site-b>:8090
```

แบบ C:

```bash
cpubench compare --latest --baseline <เครื่องที่ใช้งานจริง> -o report.html results-site-a/ results-site-b/
```

**เมื่อทดสอบคนละที่ ต้องคุมให้เท่ากันมากกว่าเดิม** (report จะเตือนเมื่อเจอสิ่งที่ไม่ตรงกัน):

| ต้องเท่ากัน | report ตรวจให้อัตโนมัติหรือไม่ |
|---|---|
| Ubuntu version, kernel, version ของ cpubench | ✅ `os-mismatch`, `kernel-mismatch`, `tool-mismatch` |
| ชนิดของ layer (native / VM / Docker / K8s) | ✅ `mode-mismatch` |
| **load generator spec เดียวกัน** (หรือขนเครื่องเดียวกันไปทดสอบทุกที่) | ✅ `loadgen-differs` และตาราง environment แสดงว่าใช้ load generator เครื่องไหน |
| load generator อยู่ใกล้ server พอ ๆ กัน (switch เดียวกัน, NIC ความเร็วเท่ากัน) | ⚠️ ดูเองจาก **p50 ของ step แรก** ใน web test ถ้าต่างกันหลาย ms แปลว่า network ต่างกัน |
| profile, SLO และขนาด VM / pod / container | ⚠️ ใช้ปุ่มและค่าเดียวกันทุกที่ |
| ช่วงเวลาที่ทดสอบ (เครื่องไม่มีงานอื่นรัน) | ⚠️ ดูได้จาก CV% ถ้า noisy ให้ทดสอบซ้ำ |

**ผล micro, stress และ power curve ไม่ขึ้นกับ network** จึงเทียบข้ามที่ได้ตรง ๆ ต้องระวังเฉพาะผล web ส่วนตารางเปรียบเทียบ layer (native/VM/Docker/K8s) เป็นการเทียบภายในเครื่องเดียวกัน จึงไม่ได้รับผลกระทบจากการอยู่คนละที่

## 9. วัด VM / container (overhead ของแต่ละ layer)

ติดตั้ง node ใน Proxmox VM บน `arm-01` (สร้าง VM ด้วย `deploy/proxmox/create-bench-vm.sh`) แล้วตั้งชื่อ node ต่างจาก host แต่ใส่ `--machine` เป็นชื่อ host:

```
ExecStart=/usr/local/bin/cpubench node --hub http://<lg01-ip>:8090 --name arm-01-vm --machine arm-01 --power redfish://<bmc-user>@<bmc-ip>
```

- VM อ่านค่าไฟของ host ผ่าน Redfish ทาง network ได้ ถ้า VM เข้าถึง BMC ได้
- เมื่อกด **standard** ที่ `arm-01-vm` แล้ว report จะแสดงส่วน "Virtualisation / container overhead" ว่า VM ช้ากว่า native เท่าไหร่
- Docker และ K8s ทำแบบเดียวกันได้ ดูตัวอย่างใน `deploy/docker` และ `deploy/k8s`

## 10. แก้ปัญหา

| อาการ | สาเหตุ / วิธีแก้ |
|---|---|
| node ไม่ขึ้นใน hub | `journalctl -u cpubench-node` → ถ้าเห็น `401` แปลว่า token ไม่ตรง ถ้า `connection refused` ให้ตรวจ firewall port 8090 และ URL ของ hub |
| node ขึ้น offline | node ไม่ได้ poll นานเกิน 60 วินาที ให้ตรวจว่า service ยังรันอยู่หรือไม่ |
| กดปุ่มแล้วได้ `cross-site request refused` | ต้องเปิดหน้าเว็บผ่าน URL ของ hub โดยตรง ไม่ใช่ผ่าน proxy ที่เปลี่ยน Host header |
| `redfish: GET /redfish/v1/Chassis: 401` | user ใน BMC ไม่มีสิทธิ์ Redfish หรือ password ผิด |
| `redfish: no chassis reports power` | firmware ของ BMC อาจไม่มี Power resource ให้ใช้ `ipmi://` แทน |
| web test fail ตั้งแต่ step แรก | ตรวจว่า lg01 เข้าถึง `http://<sut-ip>:8080/healthz` ได้หรือไม่ และ firewall บน SUT เปิด port 8080 หรือยัง |
| web test มี `dropped` เยอะ | load generator ยิงไม่ทัน ให้ใช้เครื่องที่แรงกว่า หรือเพิ่ม agent (`cpubench web agent` + `web load --agents`) |
| ผลแกว่งมาก (CV > 5%) | ดู `doctor` → ตรวจ governor, power policy และงานอื่นที่รันอยู่บนเครื่อง |
| `checksum differs` บน float workload | arm64 ใช้ fused multiply-add ทำให้ผลต่างกันเล็กน้อย ถือว่าปกติ แต่ถ้าเกิดกับ int หรือ crypto ต้องตรวจสอบ |

## 11. ความปลอดภัย

- hub ส่งได้เฉพาะชนิดงานที่กำหนดไว้ (auto, web-serve, web-load) **ไม่สามารถสั่งคำสั่ง shell บน node ได้**
- token อยู่ใน `/etc/cpubench*.env` (สิทธิ์ 600) และรหัสผ่าน BMC อยู่บน node เท่านั้น ไม่ถูกส่งผ่าน hub
- node service รันเป็น root เพื่ออ่าน DIMM, ใช้ `ipmi://local` และ pin CPU ได้ ถ้าไม่ต้องการ ให้ใส่ `User=` ใน unit แต่จะไม่ได้ข้อมูล DIMM
- ใช้ในเครือข่ายทดสอบเท่านั้น hub ยังไม่มี TLS ถ้าต้องเข้าถึงจากนอกเครือข่าย ให้วาง reverse proxy ที่มี HTTPS ไว้หน้า hub
- เมื่อทดสอบเสร็จแล้ว ปิด service ทั้งหมดด้วย `sudo systemctl disable --now cpubench-node cpubench-hub`
