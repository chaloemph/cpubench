# cpubench

ภาษาไทย · [English](README.en.md)

เครื่องมือ benchmark CPU และ web workload สำหรับเทียบ server ทั้ง x86-64 (amd64) และ ARM64 โดยไม่ผูกกับผู้ขายหรือรุ่นใด ใช้ได้ทั้งเทียบข้ามสถาปัตยกรรม (x86 ↔ ARM ทั้งสองทิศ) และเทียบเครื่องสถาปัตยกรรมเดียวกันจากหลายผู้ขาย
เป็น binary ไฟล์เดียว ไม่มี dependency ตอนรัน ใช้ได้ทั้งแบบ native, VM, Docker และ Kubernetes และสร้าง report HTML/Markdown ได้ในตัว

วิธีทดสอบและวิธีอ่านตัวเลข (สำหรับผู้ขายและผู้ที่จะทำซ้ำ): **[docs/METHODOLOGY.md](docs/METHODOLOGY.md)**

## ติดตั้ง / Build

ดาวน์โหลด `cpubench-linux-amd64` หรือ `cpubench-linux-arm64` จาก [Releases](https://github.com/chaloemph/cpubench/releases) แล้วตรวจกับ `SHA256SUMS` หรือ build เอง:

```bash
go install github.com/chaloemph/cpubench/cmd/cpubench@latest
make build-all          # dist/cpubench-linux-amd64, dist/cpubench-linux-arm64
make image              # multi-arch image (slim + full) ต้องใช้ docker buildx
make test
```

ต้องใช้ Go 1.26+ ถ้าจะ build image ARM แบบ `full` (มี `apt-get`) บนเครื่อง x86 ต้องเปิด QEMU binfmt ก่อน ส่วน image แบบ `slim` cross-compile ได้โดยไม่ต้องใช้

## ทดสอบด้วยคำสั่งเดียว / ปุ่มเดียว

**คำสั่งเดียวต่อเครื่อง** (รัน micro → stress → power curve → density และ web ถ้าใส่ `--target`):

```bash
cpubench auto --profile standard --label arm-01 --power redfish://admin@arm-01-bmc --upload http://hub:8090
```

| profile | เวลาโดยประมาณ | ใช้ตอนไหน |
|---|---|---|
| `smoke` | ~1 นาที | เช็คว่าติดตั้งและเชื่อมต่อทุกอย่างได้ |
| `quick` | ~5 นาที | ดูผลคร่าว ๆ |
| `standard` | ~25 นาที | ผลที่ใช้ตัดสินใจ |
| `full` | ~1.5 ชั่วโมง | ไล่ thread ละเอียด + stress 30 นาที |

ได้ไฟล์ JSON หนึ่งไฟล์และ `cpubench-<label>-<profile>.html` เมื่อรันครบทุกเครื่องแล้ว ใช้คำสั่งนี้เพื่อเปรียบเทียบ โดยระบุ baseline เป็นเครื่องที่ใช้งานจริงอยู่ตอนนี้ (จะเป็น x86 หรือ ARM ก็ได้):

```bash
cpubench compare --latest --baseline x86-01 results/
```

cpubench ไม่เลือกสถาปัตยกรรมใดเป็น baseline ให้เอง ถ้าไม่ใส่ `--baseline` จะใช้ผลแรกตามลำดับที่ส่งเข้าไป และเมื่อมีมากกว่าหนึ่งเครื่อง คำสั่งจะเตือนทาง stderr และ report ขึ้นคำเตือนไว้บนสุด

**รันซ้ำหลายรอบ:** รัน profile สำหรับตัดสินใจ 2–3 รอบต่อเครื่อง ให้ทุกรอบของเครื่องเดียวกันใช้ `--machine` เดียวกันแต่ `--label` ไม่ซ้ำกัน แล้วรวมเป็นคอลัมน์เดียว:

```bash
for i in 1 2 3; do cpubench auto --profile standard --machine x86-01 --label x86-01-r$i; sleep 30; done
cpubench compare --repeats --baseline x86-01 results/
```

รอบที่ machine, layer และคำสั่ง (`auto standard`) ตรงกันถือเป็นรอบซ้ำ แต่ละแถวจะเป็น median ของทุกรอบ และ CV ในรายงานคือความแกว่งระหว่างรอบ ชื่อคอลัมน์คือส่วนที่ label ทุกรอบมีร่วมกัน (`x86-01`) และใช้ label ของรอบใดรอบหนึ่งกับ `--baseline` หรือ `--for` ได้ด้วย ถ้าไม่ใส่ `--repeats` แต่ละรอบจะเป็นคอลัมน์ของตัวเอง ส่วน `--latest` เก็บรอบเดียวต่อ label จึงใช้ร่วมกับ `--repeats` ไม่ได้ และปุ่ม compare บน hub ไม่รวมรอบให้

**ปุ่มเดียวผ่าน hub:**

```bash
cpubench hub --token <token>                                            # เครื่องกลาง
cpubench node --hub http://hub:8090 --token <token> --power redfish://admin@<bmc>       # ทุกเครื่องที่จะทดสอบ
```

เปิด `http://hub:8090` แล้วจะเห็นทุกเครื่องที่เชื่อมต่ออยู่:
- ปุ่ม **smoke / quick / standard / full** ของแต่ละเครื่อง หรือปุ่ม **Run on every online machine**
- **Web test**: เลือก server กับ load generator แล้วกด Run (hub สั่ง `web serve` ให้ server, ยิงโหลดจากอีกเครื่อง แล้วหยุด server ให้เอง)
- **Compare latest of every machine**: ได้ report เทียบทุกเครื่องทันที (ใส่ช่อง Baseline เป็นเครื่องที่ใช้งานจริง)

ปุ่มต้องใส่รหัสผ่าน (username อะไรก็ได้ password คือ token) รหัสผ่าน BMC เก็บอยู่บนแต่ละเครื่องเท่านั้น ไม่ถูกส่งผ่าน hub
ถ้าต้องการให้ node รันตลอดเวลา ดู `deploy/systemd/cpubench-node.service`

## ขั้นตอนทดสอบที่แนะนำ (สั่งทีละขั้นเอง)

ทำซ้ำแบบเดียวกันบน **ทุกเครื่องที่จะเทียบ** (เช่น `x86-01` และ `arm-01`) โดยใช้ Ubuntu เวอร์ชันเดียวกันทุกเครื่อง

```bash
# 0. ตรวจเครื่อง
cpubench info
cpubench doctor                      # governor, NUMA, DIMM, VM CPU type, ...
sudo cpupower frequency-set -g performance

# 1. micro suite (~10-20 นาที บนเครื่องใหญ่)
cpubench run --label arm-01 --threads 1,8,32,max --upload http://hub:8090

# 2. stress: sustained vs peak, throttle, error
cpubench stress --label arm-01 --duration 30m --power redfish://admin@arm-01-bmc

# 3. power curve สำหรับ TCO (อ่านไฟจาก BMC)
export CPUBENCH_BMC_PASSWORD=...
cpubench power-curve --label arm-01 --power redfish://admin@arm-01-bmc

# 4. web: รัน target บน server แล้วยิงจากเครื่อง load-gen อีกเครื่อง
cpubench web serve --addr :8080                                   # บน server
cpubench web load --target http://arm-01:8080 --label arm-01 \
  --power redfish://admin@arm-01-bmc --upload http://hub:8090   # บน load-gen

# 5. เปรียบเทียบ (label เดียวกัน = รวมผล micro + web ของเครื่องเดียวกัน)
cpubench compare --baseline x86-01 --tco examples/tco.yaml -o report.html --md report.md \
  results/*.json
```

### ทดสอบทีละ layer (native / VM / Docker / K8s)

ให้ทุก run ของเครื่องเดียวกันใช้ `--machine` เดียวกัน แล้วใช้ `--label` ต่างกัน report จะแสดง overhead ของแต่ละ layer เทียบกับ native ให้อัตโนมัติ

```bash
cpubench run --machine arm-01 --label arm-01-native          # bare metal
cpubench run --machine arm-01 --label arm-01-vm              # ใน Proxmox VM
docker run --rm cpubench:latest run --machine arm-01 --label arm-01-docker --upload http://hub:8090
kubectl apply -f deploy/k8s/micro-job.yaml                         # หรือ helm (deploy/helm/cpubench)
```

hypervisor นับเป็นส่วนหนึ่งของ layer ด้วย: VM บน VMware คือ `vm/vmware` ส่วน VM บน Proxmox คือ `vm/kvm` ถ้าเทียบข้าม hypervisor report จะเตือน (`virt-mismatch`) เพราะ CPU model (EVC กับ `cpu=host`), scheduler และ virtual device ต่างกัน ส่วนต่างที่เห็นจึงไม่ได้มาจาก hardware อย่างเดียว

ถ้าเป็น VM ให้รัน `cpubench power --power redfish://... --duration 20m --label arm-01-vm` บนเครื่องควบคุมไปพร้อมกัน เพราะ VM อ่านไฟของ host เองไม่ได้

### ทดสอบ app จริง

```bash
cpubench web load --scenario examples/scenario-real-app.yaml --label app-arm-01 --sut-stats=false
cpubench startup --cmd "node server.js" --ready http://127.0.0.1:3000/health
cpubench density --workload int/json --group 8          # จำนวน VM/pod ขนาด 8 vCPU ต่อเครื่อง
cpubench scan --arch arm64 --image registry.local/shop/api:1.4 --dir ./node_modules   # หรือ --arch amd64
```

## คำสั่งทั้งหมด

| คำสั่ง | ใช้ทำอะไร |
|---|---|
| `info`, `doctor` | ตรวจ CPU (MIDR บน ARM), NUMA, SMT, cgroup, context, BIOS, การใส่ DIMM, PCIe link ของ storage/network controller, disk และหาปัญหาที่ทำให้ผลเพี้ยน |
| `run` | micro suite: integer, float, crypto, memory (BW, latency ladder, NUMA matrix), concurrency |
| `stress` | โหลดเต็มทุก thread ต่อเนื่อง วัด sustained เทียบกับ peak, MHz, °C, W, throttle, checksum/MCE error |
| `power-curve` | ไฟที่ 0/25/50/75/100% utilisation |
| `power` | อ่านค่าไฟ (ใช้ทดสอบว่าเข้าถึง BMC ได้) หรือบันทึก trace ระหว่างที่ VM กำลังรัน |
| `web serve` / `web load` / `web agent` | web target ในตัว และ load generator แบบ open model (ramp→SLO, constant/soak, stress, spike, distributed) |
| `density` | จำนวน instance ที่ยังได้ ≥90% ของความเร็วตอนรันเดี่ยว |
| `startup` | เวลาเริ่มของ app และเวลา JIT warmup |
| `scan` | ตรวจว่า image และ binary/wheel/jar/node addon มี build ของสถาปัตยกรรมเป้าหมาย (`--arch amd64` หรือ `arm64`) หรือไม่ |
| `hub` | เก็บผลจากทุกเครื่อง และเลือกเปรียบเทียบผ่าน browser |
| `compare` / `report` | สร้าง report HTML (ไฟล์เดียว เปิด offline ได้) + Markdown + ตาราง TCO |

### อ่านผลให้ถูก

report ของ `compare` เรียงลำดับเป็น **ข้อสรุป → เงื่อนไข → ตัวเลข → สิ่งที่ตอบไม่ได้** คือขึ้นต้นด้วยประโยคเดียวว่าผลบอกอะไรและเป็นต่ออะไร (ต่อ effective CPU กี่ตัว) ตามด้วยคำเตือนที่ต้องอ่านก่อนตัวเลขทุกตัว แล้วปิดท้ายด้วยสิ่งที่ report นี้ตอบไม่ได้ คำเตือนที่ report หาเองได้มีเช่น checksum ต่างกันนอกหมวด float (แปลว่าคนละ workload), ตัวยิง web เต็มก่อน server (ตัวเลข RPS เป็นเพดานของตัวยิง), คอลัมน์อยู่คนละ layer หรือได้ CPU ไม่เท่ากัน, ทุกคอลัมน์ build ที่ ISA baseline ต่ำสุด, รันครั้งเดียวจึงไม่มีช่วงแกว่ง และ label ที่เป็น `unpinned` (ยังไม่ผูก NUMA ทำให้หมวด memory และ concurrency แกว่ง) ตารางแยกตามหมวดแสดงเป็นเปอร์เซ็นต์ พร้อมช่วงของแถวที่ต่ำสุดถึงสูงสุดในวงเล็บ หมวดหนึ่งรวมหลายหน่วยจึงไม่มีค่าจริงของหมวด คลิกชื่อหมวดเพื่อไปที่ค่าที่วัดได้จริงของทุกเครื่องในหมวดนั้น ท้าย report มีหัวข้อ "ความหมายของคำที่ใช้ในรายงาน" อธิบายคำว่า pinned/unpinned เมื่อ label ใช้คำนี้

การ์ดสรุปบนสุดขึ้นต้นด้วย **Single-thread** (geomean ของ workload แบบ throughput ที่ 1 thread) คู่กับ **Multi-thread ทั้งเครื่อง** (workload ชุดเดียวกันที่จำนวน thread สูงสุดของแต่ละเครื่อง) ทั้งสองค่าเป็นอัตราส่วนเทียบ baseline และไม่รวม memory latency, `sync/atomic-contended` และ `sync/mutex` ค่า single-thread ต้องมีผลที่ 1 thread ซึ่งทุก profile ของ `auto` มีอยู่แล้ว ถ้าสั่ง `run --threads` เองให้ใส่ `1` ด้วย `compare` พิมพ์สองค่านี้ทาง stdout เป็นบรรทัด `single:<label>` และ `multi:<label>`

หัวข้อ **ทั้งเครื่อง** เทียบแต่ละเครื่องที่จำนวน thread สูงสุดของตัวเอง แล้วคิดต่อเครื่อง ต่อ core ต่อ socket ต่อวัตต์ และต่อราคา ใช้เมื่อเครื่องมี core ไม่เท่ากัน ซึ่งตารางแยกตามหมวดตอบไม่ได้เพราะเทียบที่จำนวน thread เท่ากัน
- ต่อ socket คิดเฉพาะ run ที่ได้ทั้งเครื่องจริง (bare metal)
- ต่อวัตต์ใช้กำลังไฟเฉลี่ยของ `stress` หรือขั้นโหลดเต็มของ `power-curve` จึงต้องรัน `auto` แบบมี `--power` หรือรันสองคำสั่งนี้แยก
- ต่อราคาใช้ `server_price` จากไฟล์ `--tco` และคิดเฉพาะเครื่องที่ใช้สกุลเงินเดียวกับ baseline
- ไม่รวม `sync/atomic-contended` และ `sync/mutex` เพราะผลรวมของสองงานนี้ไม่เพิ่มตามขนาดเครื่อง

ภาษาของ report ตั้งด้วย `cpubench compare --lang th|en` ค่าเริ่มต้นคือ `th` ศัพท์เทคนิค (RPS, p99, SLO, vCPU, NUMA, RAM) ชื่อ label และชื่อ benchmark คงเป็นภาษาอังกฤษทั้งสองภาษา ข้อความที่มาจากกฎของ `doctor` (เช่น "VM does not expose AVX2") ยังเป็นภาษาอังกฤษ เพราะมาจาก domain และคำสั่ง `cpubench doctor` ใช้ข้อความชุดเดียวกัน

- ทุกไฟล์ผลบันทึก **build** ของ cpubench (version, commit, Go, `GOAMD64`/`GOARM64`) ดูได้ด้วย `cpubench version` และในแถว Build ของ report
  `compare` เตือนเมื่อสองเครื่องใช้ binary คนละ version/commit หรือ build ด้วยระดับ ISA ต่างกัน ให้ใช้ `make build-all` จาก commit เดียวกันทุกเครื่อง
- แถว crypto แสดง **implementation ที่ใช้จริง** (`hardware` = AES-NI/SHA-NI/ARMv8 crypto, `simd`, `asm`, `software` = Go ล้วน)
  - ถ้าสองเครื่องใช้ต่างกันจะติดป้าย `implementation differs` และมีคำเตือน แปลว่าส่วนต่างในแถวนั้นมาจากชุดคำสั่งที่มี ไม่ใช่ความเร็ว CPU โดยรวม
  - `doctor` บอก extension ที่ CPU ไม่มี (เช่น core ARMv8.2 อย่าง Neoverse N1 ไม่มี SHA512 และ SVE, Xeon รุ่นก่อน Ice Lake ไม่มี SHA-NI)

### ส่ง report ให้ผู้ขาย

report ปกติมีผลของผู้ขายทุกราย ถ้าจะส่งให้รายใดรายหนึ่ง ให้สร้างสำเนาด้วย `--for`:

```bash
cpubench compare --baseline current-vm --for vendora01 --lang en -o report-vendora.html results/*.json                      # ตัดเครื่องอื่นออก
cpubench compare --baseline current-vm --for vendora01 --others anonymize --lang en -o report-vendora.html results/*.json   # เก็บตัวเลขเครื่องอื่นไว้แบบไม่ระบุชื่อ
```

- `--for` รับ label หรือ machine คั่นด้วยจุลภาค ทุก run ของเครื่อง baseline จะอยู่ในสำเนาเสมอ
- `--others drop` (ค่าเริ่มต้น) ตัดเครื่องอื่นออกทั้งหมด เป็นแบบที่ปลอดภัยที่สุด
- `--others anonymize` เปลี่ยนชื่อเครื่องอื่นเป็น `candidate-a`, `candidate-b`, ... และลบ hostname, รุ่น CPU, feature, แพลตฟอร์ม, BIOS, part ของ DIMM และ disk, PCI device, URL, ที่อยู่ BMC และราคาออก รวมทั้งในข้อความ finding และ kernel log
- ราคาจากไฟล์ `--tco` ของเครื่องอื่นไม่ถูกส่งออกไป และที่อยู่ BMC ของทุกเครื่องในสำเนาเหลือแค่ชนิด (`redfish`, `ipmi`)
- สถาปัตยกรรมยังแสดงอยู่ ถ้ามีผู้ขายสถาปัตยกรรมนั้นเพียงรายเดียว คอลัมน์ที่ซ่อนชื่อแล้วก็ยังบอกได้ว่าเป็นใคร
- ใช้ได้กับคำสั่ง `compare` เท่านั้น ปุ่ม compare บน hub ยังไม่มีโหมดนี้

แหล่งไฟที่รองรับ (`--power`): `redfish://user@bmc`, `ipmi://local`, `ipmi://user@bmc`, `rapl`, `hwmon[:name]`, `const:W`
(รหัสผ่านอ่านจาก `$CPUBENCH_BMC_PASSWORD`)

## Deploy

- `deploy/docker/compose.yaml`: node (กดปุ่มจาก hub), SUT, load-gen, micro, hub ดูวิธีใช้ใน [docs/DOCKER.md](docs/DOCKER.md)
- `deploy/k8s/`: Job สำหรับ micro, Deployment + NodePort สำหรับ web SUT, hub
- `deploy/helm/cpubench`: chart ที่เลือก `mode` (node/auto/micro/stress/sut/hub) และ `arch` ได้ ดูวิธีใช้ใน [docs/K8S.md](docs/K8S.md)
- `deploy/proxmox/create-bench-vm.sh`: สร้าง VM ที่ตั้ง `cpu=host`, NUMA affinity, ปิด balloon, virtio multiqueue, UEFI บน ARM (โหมด dry-run เป็นค่าเริ่มต้น)
- `scripts/collect-info.sh`, `scripts/capture-fixture.sh`: เก็บข้อมูลเครื่อง / fixture สำหรับ test

คู่มือติดตั้ง: [bare metal + hub](docs/INSTALL.md) · [Docker](docs/DOCKER.md) · [Kubernetes](docs/K8S.md)
ทุกคู่มือมีฉบับภาษาอังกฤษด้วย (ชื่อไฟล์เดียวกันต่อท้าย `.en`)

ดูสถาปัตยกรรมและวิธีพัฒนาต่อได้ที่ [DESIGN.md](DESIGN.md) · ก่อนเปิด hub ให้คนอื่นเข้าถึง อ่าน [SECURITY.md](SECURITY.md)

## License

[Apache License 2.0](LICENSE)
