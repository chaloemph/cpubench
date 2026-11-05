# cpubench design

เอกสารนี้เขียนให้คนในทีม **และ agent** ที่จะมาพัฒนาต่ออ่านแล้วเริ่มงานได้เลย โดยไม่ต้องอ่านประวัติการคุยก่อนหน้า

## เป้าหมาย

วัดและเทียบ server ทั้ง x86-64 (amd64) และ ARM64 แบบเป็นกลาง ไม่ผูกกับผู้ขาย รุ่น CPU หรือทิศทางการย้าย ใช้ตอบได้ทั้ง "ย้ายข้ามสถาปัตยกรรมได้ไหม" และ "ผู้ขายรายไหนคุ้มกว่าในสถาปัตยกรรมเดียวกัน" สำหรับ web API / web app บน bare metal, VM, Docker หรือ Kubernetes โดยใช้ข้อมูลเหล่านี้:

1. **ประสิทธิภาพ**: micro (ต่อ core / ต่อเครื่อง), web (Max RPS @ SLO, p99 ที่โหลดต่ำ)
2. **ความคุ้มค่า**: RPS/W, power curve, จำนวน server ที่ต้องใช้, TCO 5 ปี
3. **ความเสถียร**: stress, soak, spike, checksum, MCE
4. **ความพร้อม**: overhead ของแต่ละ layer (native/VM/Docker/K8s), density, startup/warmup, image/binary ที่มี build ของสถาปัตยกรรมเป้าหมาย

## สถาปัตยกรรม: hexagonal-lite

```
cmd/cpubench            composition root: ประกอบ adapter → app → cli
internal/
  domain/               ❶ pure logic ห้ามมี I/O
    hardware/           System, Topology, PlacementOrder, MIDR lookup
    benchmark/          Workload interface, Result, Stats
    doctor/             กฎตรวจ environment (pure function ของ hardware.System)
    compare/            speedup, geomean, layer overhead, TCO
    load/               histogram (HDR-style), Scenario/Phase, SLO, knee, recovery
    scan/               image/binary รันบนสถาปัตยกรรมเป้าหมาย (amd64/arm64) ได้หรือไม่
    result/             schema ของไฟล์ผล "cpubench/v1"
  port/                 ❷ interface ทั้งหมดที่ app ใช้คุยกับภายนอก
  app/                  ❸ use cases: Run, Stress, PowerCurve, NUMAMatrix, Load, Density, Startup, Scan, RecordPower
  workload/             ❹ kernel ที่ถูกวัด (ไม่ใช่ adapter)
  adapter/in/           ❺ driving: cli, websut (target server), httpapi (agent, hub)
  adapter/out/          ❻ driven: probe/linux, affinity/linux, power, store/jsonfile, report, httpload, hubclient, process, scanner
  archtest/             test ที่บังคับกติกา dependency (แทน depguard)
```

กติกา dependency (บังคับโดย `internal/archtest`):

| package | ห้าม import |
|---|---|
| `domain/**` | `os`, `os/exec`, `net/*`, `syscall`, `x/sys`, app, port, adapter, workload |
| `port` | app, adapter |
| `app` | adapter, `os/exec`, `net/http` |
| `workload` | `os`, `net/*`, app, adapter |
| `adapter/out/**` | `adapter/in`, app |

ข้อตกลงอื่น:
- **ห้ามมี interface ใน hot loop**: `Workload.RunThread` เรียกครั้งเดียวต่อ iteration ต่อ thread ส่วน loop ข้างในเป็นโค้ด concrete ทั้งหมด
- **Workload ต้อง deterministic**: งานปริมาณคงที่ต่อการเรียก และ checksum ต้องเท่ากันทุกครั้งที่ใช้ id เดียวกัน ผลของ float ถูกปัดเป็น 6 หลักก่อน hash เพราะ arm64 ทำ fused multiply-add แต่ amd64 ไม่ทำ
- **การ pin thread**: goroutine เรียก `LockOSThread` แล้ว `sched_setaffinity` และ**จบโดยไม่ unlock** เพื่อให้ runtime ทิ้ง thread ที่ถูกจำกัด affinity ไป ไม่นำกลับมาใช้ซ้ำ
- **ลำดับ CPU**: `PlacementOrder` ให้ใช้ physical core ก่อน (ไล่ทีละ NUMA node) แล้วจึงใช้ SMT sibling ดังนั้น "8 threads" หมายถึง 8 core ทั้งบน x86 และ ARM
- **จำนวน CPU ที่ใช้ได้จริง**: คิดจาก affinity ∩ cgroup cpuset ∩ ceil(cpu.max quota) ไม่ใช่ `nproc`
- **layer**: `System.Layer()` คือ `Context.Mode` ต่อด้วย hypervisor เมื่ออยู่ใน VM (`vm/kvm`, `vm/vmware`, `container/vmware`) report และ `doctor.Comparable` ใช้ค่านี้ ไม่ใช่ `Mode` อย่างเดียว เพราะ VM บน VMware กับบน Proxmox ต่างกันทั้ง CPU model, scheduler และ device

## ขั้นตอนการวัด

| ส่วน | วิธี |
|---|---|
| micro | warmup 1 รอบ (และเป็นการ first-touch memory ด้วย) จากนั้นวัด N รอบโดยปล่อยทุก thread พร้อมกันด้วย barrier แล้วรายงาน median และ CV% (เกิน 5% จะ flag ว่า noisy) |
| memory BW | strong scaling: working set รวมคงที่แล้วแบ่งให้แต่ละ thread และแต่ละ thread first-touch ส่วนของตัวเอง หน้า memory จึงอยู่ NUMA node เดียวกับ thread |
| NUMA matrix | สร้าง pointer chain บน thread ที่ pin ไว้ใน node M (first-touch) แล้ว chase จาก node C |
| web | open model: request แต่ละตัวมีเวลาส่งที่กำหนดไว้ (intended time) และวัด latency จากเวลานั้น จึงไม่เกิด coordinated omission ถ้า load-gen ตามไม่ทันเกิน deadline จะนับเป็น dropped และถือว่า SLO fail |
| knee | Max RPS @ SLO = achieved RPS สูงสุดของ step ที่ผ่าน p99 ≤ SLO, error ≤ SLO และ achieved ≥ 95% ของ target |
| distributed | coordinator แบ่ง rate ให้ agent เท่า ๆ กัน และกำหนดเวลาเริ่มพร้อมกัน จากนั้นรวม histogram เข้าด้วยกัน (histogram แบบนี้ merge กันได้) |
| power | ใช้ Redfish (`/Chassis/*/Power` หรือ `EnvironmentMetrics`), IPMI DCMI, RAPL หรือ hwmon และ sample ทุก 1 วินาทีแล้วแบ่งตามช่วงเวลาของแต่ละ step |
| TCO | servers = ceil(target / knee) + redundancy และ total = capex + W × PUE × 8760 / 1000 × rate × ปี |
| ทั้งเครื่อง | `compare.Whole`: แต่ละคอลัมน์ใช้ผลที่จำนวน thread สูงสุดของแต่ละ workload แบบ throughput (ไม่รวม single-thread, latency, `sync/atomic-contended`, `sync/mutex`) แล้วหา geomean ของอัตราส่วนเทียบ baseline จากนั้นหารด้วย core ที่ใช้ได้ (`System.UsableCores`), socket (เฉพาะ run ที่ได้ทั้งเครื่อง), วัตต์ตอนโหลดเต็ม (`Stress.AvgWatts` หรือขั้น 100% ของ power curve) และราคา (`server_price` จาก TCO สกุลเงินเดียวกับ baseline) |
| baseline | `app.DefaultBaseline` ใช้ผลแรกตามลำดับที่ส่งเข้ามา **ไม่เลือกตามสถาปัตยกรรม** ส่วน `AmbiguousBaseline` คืนรายชื่อเครื่องเมื่อมีมากกว่าหนึ่งเครื่อง เพื่อให้ CLI และ report เตือนให้ระบุ `--baseline` |

## คำสั่งเดียว / ปุ่มเดียว

- `app.Auto` ร้อยทุกขั้นตอน (micro → stress → power-curve → density → web) ตาม `app.Profiles` (smoke/quick/standard/full) แล้วรวมเป็นไฟล์ผลเดียว
- **hub control**: `domain/control.Board` เป็น state machine ที่ไม่มี I/O มีหน้าที่:
  - รู้ว่ามี node ไหนเชื่อมต่ออยู่
  - จัดคิว job และส่งให้ทีละ job ต่อ node
  - จับคู่ web test: งาน `web-serve` บน server ต้อง report stage `ready` ก่อน งาน `web-load` จึงถูกปล่อย และเมื่อ load จบ hub จะสั่งหยุด serve
- hub (`adapter/in/httpapi`) ห่อ Board ด้วย mutex และ long-poll ส่วน node (`adapter/in/cli/node.go`) poll hub, รัน use case, upload ผล แล้ว report สถานะกลับ
- **ความปลอดภัย**:
  - job เป็นชนิดที่กำหนดไว้แล้วเท่านั้น hub ส่งคำสั่ง shell ไม่ได้
  - API ต้องใช้ Bearer token ส่วนปุ่มใช้ Basic auth (password = token) และตรวจ same-origin เพื่อกัน CSRF
  - credential ของ BMC อยู่บน node เท่านั้น
- SUT ส่งค่าไฟของตัวเองผ่าน `/_cpubench/stats?power=1` load generator จึงคำนวณ RPS/W ได้ แม้จะเข้าถึง BMC ของ server ไม่ได้

## ข้อมูล platform

การเทียบ x86 ต่างยี่ห้อที่ใช้ CPU ตระกูลเดียวกัน ความต่างมักมาจากสิ่งที่ผู้ขายประกอบมา probe จึงเก็บข้อมูลเหล่านี้ไว้ใน `System` ด้วย:

- `Platform.BIOSVendor`, `BIOS`, `BIOSDate` จาก `/sys/class/dmi/id`
- `Memory.Slots` (ทุกช่องที่ firmware รายงาน รวมช่องว่าง) และ `DIMM.Type`, `Configured`, `Rank`, `Manufacturer` จาก `dmidecode -t 17` (ต้องเป็น root)
- `PCI`: storage และ network controller (PCI class `01xxxx`/`02xxxx`) พร้อม driver, NUMA node, link speed/width ปัจจุบันและสูงสุด
- `Disks`: block device ที่มี hardware อยู่ข้างหลัง (ไม่รวม loop, dm, md, zram)

กฎ doctor ที่ใช้ข้อมูลนี้:

| กฎ | เงื่อนไข |
|---|---|
| `dimm-channels` | bare metal ใส่ DIMM น้อยกว่าครึ่งของช่องต่อ socket (server มีไม่เกิน 2 DIMM ต่อ channel ถ้าทุก channel มี DIMM จะใส่อย่างน้อยครึ่งหนึ่ง) |
| `dimm-mixed` | DIMM ขนาด ความเร็ว หรือ part ไม่เหมือนกัน |
| `dimm-speed` | configured speed ต่ำกว่า rated speed |
| `pcie-link` | bare metal มี controller ที่ link speed หรือ width ต่ำกว่าค่าสูงสุด |
| `virt-mismatch` (`Comparable`) | สองคอลัมน์อยู่ใน VM คนละ hypervisor |

`scripts/capture-fixture.sh` เก็บไฟล์เหล่านี้ด้วย fixture `arm64-2s-synthetic` มีตัวอย่าง NIC, SAS, NVMe และ bridge (ซึ่งต้องถูกข้าม)

## สำเนาสำหรับส่งผู้ขาย

`app.ShareCopy` (เรียกจาก `compare --for <label|machine> [--others drop|anonymize]`) สร้างชุดผลใหม่โดยไม่แก้ไฟล์เดิม:

- เก็บทุก run ของเครื่องที่ระบุและของเครื่อง baseline
- เครื่องอื่นถูกตัดออก (`drop`) หรือเปลี่ยนชื่อเป็น `candidate-a`, ... (`anonymize`) และลบสิ่งที่ระบุตัวผู้ขายหรือราคาที่เสนอ ทั้งใน field ที่มีโครงสร้างและในข้อความอิสระ (finding, kernel log) ดูรายการใน `anonymize()`
- ที่อยู่ BMC ในทุกเครื่องของสำเนาเหลือแค่ชนิด และ `TCOInput.Only` ตัดราคาของเครื่องที่ไม่อยู่ในสำเนา
- `TestShareCopyAnonymizesWithoutLeaks` ตรวจว่าไม่มีชื่อ รุ่น หรือราคาหลุดอยู่ใน JSON ของสำเนา ถ้าเพิ่ม field ที่ระบุตัวเครื่องได้ใน `result.File` ต้องเพิ่มใน `anonymize()` และใน test นี้ด้วย

## หน่วยความจำใน container

- ตอนเริ่มโปรแกรม ตั้ง Go soft memory limit (`GOMEMLIMIT`) เป็น 85% ของ cgroup `memory.max` เพราะ Go ปรับแค่ GOMAXPROCS ตาม cgroup ไม่ได้ปรับเรื่อง memory
- runner ข้าม workload ที่ `Info.MemoryMB()` เกิน 50% ของ min(RAM, memory limit) แล้วบันทึกเป็น finding `mem-limit`
- เรียก `debug.FreeOSMemory()` หลังจบแต่ละ workload
- ทั้งหมดนี้เพิ่มเข้ามาหลังจากเจอ pod ขนาด 1 GiB โดน OOM kill ตอนทดสอบบน kind

## การรวมไฟล์ผล

ทุกคำสั่งเขียนไฟล์ `result.File` (schema `cpubench/v1`) หนึ่งไฟล์ คำสั่ง `compare` และ hub จะรวมไฟล์ที่มี **label เดียวกัน** เข้าด้วยกัน (เช่น micro run กับ web load ของ server เดียวกัน) และจัดกลุ่มตาม **machine** เพื่อคำนวณ overhead ของแต่ละ layer

## Build info และ backend ของ crypto

ผลที่ต่างกันอาจมาจาก binary หรือชุดคำสั่ง ไม่ใช่ความเร็ว CPU จึงบันทึกสองเรื่องนี้ไว้ในไฟล์ผล

- **`File.Build`** (`domain/build`): อ่านจาก `debug.ReadBuildInfo` ใน `cmd/cpubench` ได้แก่ version (`-X main.version`), commit (`vcs.revision`), modified, Go version, `GOAMD64`/`GOARM64`, cgo, tags และ GOEXPERIMENT
  - `build.Self` เตือนเมื่อ build จาก source ที่ยังไม่ commit
  - `build.Comparable` เตือนใน `compare` เมื่อ version/commit ต่างกัน, ระดับ ISA ต่างกันบน arch เดียวกัน, ฝั่งใดไม่ได้ใช้ระดับ default (`v1` / `v8.0`), tags หรือ GOEXPERIMENT ต่างกัน
  - ไฟล์จากเวอร์ชันเก่าที่ไม่มี `build` จะได้ finding `build-unknown` (info)
- **`Result.Backend`**: workload ที่ใช้ `benchmark.BackendReporter` (crypto ทั้งหมด) บอกว่ารันด้วย `hardware` / `simd` / `asm` / `software`
  - ใช้เงื่อนไขเดียวกับ `crypto/internal/fips140` ของ Go 1.26 โดยตัดสินจาก flag ใน `/proc/cpuinfo`
  - probe ต้องเก็บ flag ที่เงื่อนไขใช้ไว้ด้วย (`ssse3`, `sse4_1`) ซึ่ง `TestFixturesKeepCryptoFeatures` ตรวจไว้แล้ว
  - ถ้า Go เวอร์ชันใหม่เปลี่ยนเงื่อนไข ต้องแก้ `internal/workload/crypto.go`
- `compare` ติดป้าย `implementation differs` และสร้าง finding `backend-differs` เมื่อแต่ละเครื่องรันคนละแบบ เช่น AVX2 กับ ARMv8 SHA2
- doctor rule `ruleISA` บอก extension ที่ขาด ได้แก่ AES/PMULL, SHA2, SHA512, SVE บน arm64 และ AES-NI/PCLMULQDQ, SHA-NI บน x86

## การทดสอบ

- `go test ./...` รันได้บนทั้ง amd64 และ arm64 (CI รันทั้งสองสถาปัตยกรรม) ไม่ต้องมีเครื่องอีกสถาปัตยกรรม
- `testdata/fixtures/*`: tree ของ /proc และ /sys ที่ probe อ่าน
  - `x86-wsl-i7-1360p` capture จากเครื่องจริง
  - `arm64-2s-synthetic` สร้างขึ้นเองเพื่อจำลอง server arm64 แบบ 2 socket 4 NUMA node
- เพิ่ม fixture จากเครื่องจริงได้ด้วย `sudo bash scripts/capture-fixture.sh testdata/fixtures/<name>`
- โค้ด HTTP ทดสอบด้วย `httptest` และ use case ทดสอบด้วย fake port

## สถานะ

ทำเสร็จทั้ง Phase 1–5 และทดสอบบน x86 (WSL2) แล้ว **สิ่งที่ยังต้องทดสอบบนเครื่องจริง**:

| เรื่อง | สิ่งที่ต้องทำ |
|---|---|
| binary arm64 | รันบน ARM64 VM (QEMU/KVM) แล้วทั้ง `auto smoke` และ `auto standard`: checksum ตรงกับ x86 VM ครบทุก workload เมื่อใช้ profile เดียวกัน (รวมงาน float)<br>**ยังไม่ยืนยัน**: bare metal arm64 |
| memory locality บน arm64 VM | ถ้า VM ผูกแค่ vCPU แต่ไม่ผูก memory กับ NUMA node เดียว หน้า memory ส่วนหนึ่งจะอยู่ node อื่น (atomic ช้า ~9 เท่า, DRAM ช้า ~2 เท่า) ตัวเลข `sync/atomic-*`, `mem/latency-*`, `mem/triad` จึงแกว่งและเทียบไม่ได้ ต้องผูกทั้ง vCPU และ memory ดู `deploy/proxmox/create-bench-vm.sh` และ [docs/METHODOLOGY.md](docs/METHODOLOGY.md) |
| fixture arm64 | ตอนนี้เป็น synthetic: ให้ capture จาก server arm64 จริง (และ x86 server จริง) แล้วตรวจ MIDR, NUMA, NIC ว่าตรงหรือไม่ |
| Redfish ของ BMC จริง | ทดสอบกับ mock ตาม Redfish spec แล้ว ยังต้องลองกับ BMC ของแต่ละผู้ขาย (iDRAC, iLO, XCC, iBMC ฯลฯ) |
| Proxmox บน ARM | script สร้าง VM ยังไม่เคยรันจริง (ค่าเริ่มต้นเป็น dry-run) |
| Docker | ทดสอบบนเครื่องนี้แล้ว: compose `node` (bridge + `HOST_IP`) → ปุ่ม auto/web test → overhead เทียบกับ run ของเครื่องเดียวกันที่อยู่ชั้นล่างสุด (ถ้าไม่มี native จะเทียบกับ VM) และ build image ARM เป็น tar ได้โดยไม่ต้องใช้ QEMU |
| K8s / Helm | ทดสอบบน kind (x86, 1 node) แล้ว: Helm mode `node` → pod เข้า hub → ปุ่ม auto/web test ผ่าน NodePort, Guaranteed QoS แต่ยังไม่ได้ทดสอบ static CPU manager และ cluster แบบ mixed-arch จริง |
| image `full` สำหรับ arm64 | ต้องใช้ buildx + QEMU หรือ build บน ARM runner |

## แบ่งงานต่อ (สำหรับ agent)

แต่ละงานด้านล่างแยกกันทำได้ และไม่แก้ไฟล์ชนกัน:

| งาน | ไฟล์หลัก | เงื่อนไขตรวจรับ |
|---|---|---|
| เพิ่ม workload (เช่น protobuf, brotli, regex แบบ RE2-heavy) | `internal/workload/*.go` + ลงทะเบียนใน `All()` | ผ่าน `TestWorkloadsDeterministic` |
| fixture server จริง (x86 และ arm64) + เติม MIDR | `testdata/fixtures/`, `domain/hardware/derive.go` | test ใน `probe/linux` ผ่านกับ fixture ใหม่ |
| กฎ doctor ใหม่ | `domain/doctor/doctor.go` | มี unit test ต่อกฎ |
| reference stacks (Node/Java/PHP/Python) | `deploy/stacks/<runtime>/` Dockerfile + endpoint ชุดเดียวกับ `web serve` | `web load --endpoint` ใช้งานได้ |
| report: chart ที่ interactive | `adapter/out/report` | `TestRenderEverySection` ผ่าน และยังเปิด offline ได้ |
| CI: build image แล้ว push ไป GHCR | `.github/workflows/` (test + build-all บน amd64/arm64 และ release binary มีแล้ว) | image ทั้ง `slim` และ `full` ขึ้น registry เมื่อ push tag |
