# วิธีทดสอบ

ภาษาไทย · [English](METHODOLOGY.en.md)

เอกสารนี้อธิบายว่า cpubench วัด server อย่างไร และเราเทียบเครื่องอย่างไร เพื่อให้
ผู้ขายและผู้ใช้อื่นทำซ้ำได้และอ่านรายงานได้เหมือนเรา คำสั่งและ flag ดูใน
[README](../README.md) ([English](../README.en.md))

## 1. การทดสอบนี้ตอบคำถามอะไร

เป้าหมายคือการตัดสินใจเรื่องขนาดระบบหรือการย้ายเครื่อง: fleet ปัจจุบันย้ายไป server
อีกแบบได้หรือไม่ (ต่างผู้ขาย ต่างรุ่น หรือต่างสถาปัตยกรรม x86-64 ↔ ARM64 ได้ทั้งสองทิศ)
และต้องใช้กี่เครื่อง กี่วัตต์ กี่บาท เราตอบด้วยมุมมอง 3 แบบ และรายงานไม่เคยปนกัน:

| มุมมอง | เทียบอะไร | ตอบคำถาม |
|---|---|---|
| ต่อ core | แต่ละ benchmark ที่ **จำนวน thread เท่ากัน** (1, จำนวน physical core, ทุก CPU) | 1 core ของ B เร็วหรือช้ากว่า 1 core ของ A? |
| ทั้งเครื่อง | แต่ละเครื่องที่จำนวน thread **สูงสุดของตัวเอง** | server หนึ่งเครื่องทำงานได้เท่าไหร่ ต่อ socket ต่อวัตต์ ต่อราคา? |
| Web | โหลด HTTP แบบ open model จนกว่า SLO จะหลุด | server รับได้กี่ request ต่อวินาทีภายใต้ขีดจำกัด p99 และ error? |

ผลต่อ core ของ VM เล็กๆ บอกอะไรเกี่ยวกับ server 2 socket ทั้งเครื่องไม่ได้ และกลับกัน
รายงานจึงระบุเสมอว่าตัวเลขแต่ละตัวอยู่ในมุมมองไหน

## 2. กติกาที่ทำให้สองการรันเทียบกันได้

`cpubench doctor` ตรวจเกือบทุกข้อ และ `compare` เตือนเมื่อสองคอลัมน์ฝ่าฝืนข้อใดข้อหนึ่ง

1. **binary เดียวกัน** build ด้วย `make build-all` จาก commit เดียว แล้ว copy binary
   ตัวเดียวกันไปทุกเครื่อง image Docker ใช้ Go รุ่นเดียวกับ `go.mod` ทุกผลบันทึก
   version, commit, เวอร์ชัน Go และระดับ ISA (`compare` เตือน `tool-mismatch`)
2. **ระดับ ISA เท่ากัน** binary ที่ปล่อยถูก build ที่ระดับต่ำสุดของแต่ละสถาปัตยกรรม
   (`GOAMD64=v1`, `GOARM64=v8.0`) ทั้งสองฝั่งไม่ใช้ AVX-512 หรือ SVE/SVE2 เป็นเงื่อนไข
   ที่เท่ากัน แต่ไม่ใช่ศักยภาพสูงสุดของ CPU แต่ละตัว crypto ของ Go ยังใช้ AES-NI /
   SHA-NI / ARMv8 crypto เมื่อ CPU มี และรายงานแสดง implementation ที่ใช้ทุกแถวของ crypto
3. **OS และตระกูล kernel เดียวกัน** distribution release เดียวกันทุกเครื่อง
   (`os-mismatch`, `kernel-mismatch`)
4. **layer เดียวกัน** เทียบ native กับ native, VM กับ VM, container กับ container
   (`mode-mismatch`) hypervisor นับเป็นส่วนของ layer: VM บน VMware กับ VM บน KVM
   เป็นคนละ layer (`virt-mismatch`)
5. **นโยบายความถี่ CPU** ตั้ง governor เป็น `performance` บน bare metal
   (`sudo cpupower frequency-set -g performance`) ถ้าไม่ตั้ง `doctor` จะเตือน
6. **ชนิด CPU ของ VM** VM บน x86 ต้องเปิดเผย CPU ของ host (`cpu=host` บน Proxmox
   หรือ EVC mode สูงสุดบน VMware) CPU type ทั่วไปอย่าง `kvm64` ซ่อน AVX2, AES-NI
   และ SHA-NI ทำให้ตัวเลข crypto และ compression ไม่มีความหมาย (`vm-cpu-type`)
7. **การวาง NUMA ของ VM** ผูก vCPU **และ** memory ของ guest ไว้ใน NUMA node เดียวของ
   host การผูกแค่ vCPU ไม่พอ: memory ของ guest ส่วนหนึ่งอาจไปอยู่ node อื่น ซึ่งใน
   การทดสอบของเรากับ VM ARM64 พบว่า atomic ช้าลงราว 9 เท่า และ DRAM load ช้าลงราว
   2 เท่า และแถว memory/concurrency แกว่งหลายสิบเปอร์เซ็นต์ระหว่างรอบ
   `deploy/proxmox/create-bench-vm.sh` ตั้งค่านี้ให้ (รวม `balloon=0` และ virtio multiqueue)
8. **การใส่ memory** bare metal ควรใส่ DIMM ครบทุก channel เป็น DIMM เหมือนกันและ
   ทำงานที่ความเร็วตามสเปก `doctor` รายงาน channel ที่ไม่เท่ากัน DIMM ที่ไม่เหมือนกัน
   และ DIMM ที่ทำงานต่ำกว่าความเร็วตามสเปก
9. **ไม่มีงานอื่นรัน** ผลใช้ได้ก็ต่อเมื่อเครื่องว่างนอกจาก cpubench บน Kubernetes
   ให้ใช้ Guaranteed QoS และ static CPU manager เพื่อให้ pod ได้ core เต็มดวง
10. **ทำซ้ำ** รัน profile สำหรับตัดสินใจอย่างน้อย 2–3 ครั้งต่อเครื่อง ใช้ `--machine`
    เดียวกันและ label ต่างกันทุกรอบ (`x86-01-r1`, `x86-01-r2` …) แล้วเทียบด้วย
    `--repeats` รอบเหล่านั้นจะรวมเป็นคอลัมน์เดียว แต่ละแถวเป็น median ของทุกรอบ และ CV
    คือความแกว่งระหว่างรอบ แถวที่ CV เกิน 5% จะถูกติดธง ถ้าไม่ใส่ `--repeats` แต่ละรอบ
    เป็นคอลัมน์ของตัวเอง และ CV คือความแกว่งระหว่าง iteration ภายในรอบนั้นเท่านั้น

## 3. การรัน 1 ครั้งวัดอะไร

`cpubench auto --profile <p>` รันทุกขั้นตอนด้านล่างบนเครื่องหนึ่ง แล้วเขียนไฟล์ผล JSON
หนึ่งไฟล์กับรายงาน HTML

| Profile | เวลา | จำนวนรอบ | จำนวน thread | Stress | Power curve |
|---|---|---|---|---|---|
| `smoke` | ~1 นาที | 2 (ขนาด 5%) | 1, ทั้งหมด | 15 วินาที | 0, 100% |
| `quick` | ~5 นาที | 3 (ขนาด 30%) | 1, ทั้งหมด | 2 นาที | 0, 50, 100% |
| `standard` | ~25 นาที | 5 | 1, physical core, ทั้งหมด | 10 นาที | 0-100% ทีละ 25% |
| `full` | ~1.5 ชม. | 5 | 1, 2, 4 … physical, ทั้งหมด | 30 นาที | 0-100% ทีละ 25% |

`standard` คือ profile สำหรับตัดสินใจ ส่วน `smoke` ใช้พิสูจน์ว่าติดตั้งเรียบร้อยเท่านั้น

### 3.1 ชุด micro (`run`)

ทุก benchmark คืนค่า checksum สองเครื่องต้องได้ checksum เท่ากันแถวนั้นจึงเทียบกันได้
(แถว float อาจต่างกันที่บิตท้ายๆ จะถูกติดธงแต่ไม่ตัดทิ้ง) thread ถูกวางบน physical core
ก่อน ทีละ NUMA node แล้วจึงวางบน SMT sibling

| หมวด | Benchmark | แทนอะไร |
|---|---|---|
| integer | `int/json`, `int/regex`, `int/sort`, `int/hashmap`, `int/compress`, `int/decompress` | logic ของแอปทั่วไป: parse, จับคู่, map, บีบอัด |
| float | `float/matmul`, `float/nbody`, `float/fft` | โค้ดคำนวณตัวเลข |
| crypto | `crypto/aes-gcm`, `crypto/sha256`, `crypto/ecdsa-p256`, `crypto/rsa2048-sign` | เข้ารหัสข้อมูล TLS, hash, TLS handshake |
| memory | `mem/triad`, `mem/copy` (bandwidth), `mem/latency-32K … 512M` (pointer chase), NUMA matrix (`standard`/`full`) | พฤติกรรมของ cache และ DRAM, ต้นทุนข้าม node |
| concurrency | `sync/atomic-local`, `sync/atomic-contended`, `sync/mutex`, `sync/pingpong` | โค้ดที่ใช้ lock หนัก, การสื่อสารข้าม core |

สรุปแต่ละหมวดด้วย geometric mean ของอัตราส่วนรายแถว และรายงานแสดงแถวต่ำสุดกับสูงสุด
ไว้ข้างๆ เสมอ ค่าเฉลี่ยของหมวดซ่อนความต่างขนาดใหญ่ได้ (เช่น SHA-256 กับ RSA อาจไป
คนละทิศ) ดังนั้นให้อ่านรายแถวก่อนอ้างตัวเลขของหมวด

### 3.2 Stress (`stress`)

ทุก thread ทำงานเต็มที่ตลอดเวลาที่ profile กำหนด รายงาน throughput ที่ต่อเนื่องเทียบกับ
ค่าสูงสุด, clock, อุณหภูมิ, กำลังไฟ, การ throttle, checksum error และข้อความ
machine-check (MCE) จาก kernel log ค่า "iterations per second" ของ stress รวม benchmark
ที่ยาวไม่เท่ากัน จึงใช้ดู **ความเสถียร** ไม่ใช่อัตราส่วนความเร็ว

### 3.3 กำลังไฟ (`power-curve`, `--power`)

อ่านกำลังไฟจาก BMC ผ่าน Redfish (`redfish://user@bmc`), IPMI, RAPL หรือ hwmon ที่โหลด
0/25/50/75/100% VM อ่านกำลังไฟของ host ไม่ได้: ให้รัน `cpubench power` บนเครื่องควบคุม
พร้อมกันแทน ค่าต่อวัตต์ในมุมมองทั้งเครื่องมาจากค่าเฉลี่ยของ stress หรือขั้น 100% ของ
power curve ดังนั้นการรันที่ไม่มี `--power` จะไม่มีคอลัมน์ต่อวัตต์

### 3.4 Density (`density`)

เครื่องรัน instance ขนาดคงที่ (ค่าเริ่มต้น 8 thread ซึ่งเป็นขนาด VM หรือ pod ทั่วไป) ได้
กี่ตัว โดยแต่ละตัวยังได้อย่างน้อย 90% ของความเร็วตอนรันเดี่ยว

### 3.5 Web (`web serve` + `web load`)

`web serve` คือ HTTP target ในตัวที่ใช้โค้ดเดียวกันทุกสถาปัตยกรรม `web load` คือ load
generator แบบ open model: ส่ง request ตามอัตราคงที่ไม่ว่า request ก่อนหน้าจะกลับมาหรือไม่
server ที่ช้าจึงปรากฏเป็น latency และ error ไม่ใช่อัตรา request ที่ต่ำลง

- **Ramp** (ค่าเริ่มต้น): เพิ่มอัตราทีละขั้นจนกว่า p99 latency (ค่าเริ่มต้น 100 ms)
  หรืออัตรา error (ค่าเริ่มต้น 0.1%) จะทำให้ SLO หลุด `max RPS @ SLO` คือขั้นสูงสุด
  ที่ยังผ่าน SLO และละเอียดได้เท่าขนาดขั้นเท่านั้น
- preset request **mix**: 40% JSON, 22% อ่าน cache, 3% เขียน cache, 15% HTML template,
  10% gzip, 7% parse JSON จาก POST body, 3% login (hash รหัสผ่านด้วย PBKDF2)
  มี `plaintext` ด้วย แต่มักทำให้ load generator อิ่มก่อน
- load generator ต้องไม่เป็นคอขวด ให้รันบนเครื่องแยกที่มี core พอ หรือแบ่งเครื่อง
  เดียวเป็น CPU ของ server กับ CPU ของ load (`tools/web-loopback.sh` วาง server ไว้
  CPU 0-3 และ load ไว้ 4-7) ขั้นจะนับก็ต่อเมื่อ CPU ของ **server** อิ่มก่อน รายงานเตือน
  เมื่อ load generator อิ่มก่อน server
- การทดสอบ loopback เทียบเฉพาะ CPU ของ server ไม่รวมต้นทุนของ NIC และ virtio ซึ่ง
  การทดสอบข้าม network จริงรวมไว้
- `web load --scenario` เล่นซ้ำ request ของแอปคุณเอง (`examples/scenario-*.yaml`)

## 4. ทดสอบทีละ layer

รันทุก layer ของเครื่องเดียวกันด้วย `--machine` เดียวกันและ `--label` ต่างกัน
(`arm-01-native`, `arm-01-vm`, `arm-01-docker` …) รายงานจะแสดง overhead ของแต่ละ layer
เทียบกับ layer ที่ต่ำที่สุดของเครื่องนั้น

| Layer | วิธี | หมายเหตุ |
|---|---|---|
| Bare metal | binary, `auto` | layer เดียวที่ได้ตัวเลขต่อ socket และต่อวัตต์ |
| VM | binary ใน VM | ข้อ 6 และ 7 ด้านบน อ่านกำลังไฟฝั่ง host |
| Docker | `deploy/docker/compose.yaml` | เทียบกับ native **บน VM/host เดียวกัน** เพื่อแยก overhead ของ container |
| Kubernetes | `deploy/k8s/`, `deploy/helm/cpubench` | Guaranteed QoS, static CPU manager ตรวจการวาง NUMA ของ worker เองก่อน |

## 5. อ่านรายงาน

รายงานของ `compare` เรียงจาก **ข้อสรุป → เงื่อนไข → ตัวเลข → สิ่งที่รายงานนี้ตอบไม่ได้**
ให้อ่านคำเตือนก่อนตัวเลข คำเตือนที่พบบ่อยที่สุด:

| คำเตือน | ความหมาย |
|---|---|
| checksum differs (นอกหมวด float) | สองเครื่องไม่ได้รันงานเดียวกัน แถวนั้นเทียบกันไม่ได้ |
| implementation differs (crypto) | ฝั่งหนึ่งมีชุดคำสั่ง hardware ที่อีกฝั่งไม่มี แถวนั้นเทียบชุดคำสั่ง ไม่ใช่เทียบ CPU |
| load generator saturated | ตัวเลข web คือเพดานของ load generator ไม่ใช่ของ server |
| different layers / different CPU counts | คอลัมน์เหล่านั้นไม่ใช่การเทียบแบบเท่ากัน |
| measured only once | รอบนั้นใช้ 1 iteration จึงไม่มีช่วงความแปรปรวน ควรใช้อย่างน้อย 3 |
| baseline picked automatically | ให้ส่ง `--baseline <label>` เป็นเครื่องที่คุณใช้งานอยู่ |

## 6. สิ่งที่เราขอจากผู้ขาย

เพื่อทดสอบ server ของผู้ขายภายใต้เงื่อนไขเดียวกับ fleet ปัจจุบัน:

1. เข้าถึง bare metal ของรุ่นที่เสนอ ตั้งค่าตามที่จะส่งมอบจริง (CPU, ใส่ memory ครบทุก
   channel, จดเวอร์ชัน BIOS และค่าที่ตั้งไว้)
2. user ของ BMC ที่อ่านค่ากำลังไฟผ่าน Redfish ได้
3. OS release เดียวกับ baseline ของเรา (Ubuntu LTS เว้นแต่ตกลงกันเป็นอย่างอื่น)
4. อนุญาตให้รัน profile `standard` อย่างน้อย 3 ครั้ง บวก `full` 1 ครั้ง และการทดสอบ web
   ด้วย load generator ที่แยกเครื่อง
5. ถ้า server ถูกเสนอเป็นแพลตฟอร์ม VM ด้วย ขอค่าตั้งของ hypervisor เรื่อง CPU type
   และการวาง NUMA

ผู้ขายแต่ละรายได้รับสำเนารายงานที่มีเฉพาะเครื่องของตนกับ baseline ของเรา
(`cpubench compare --for <label>`) เครื่องของผู้ขายรายอื่นจะถูกตัดออกหรือปกปิดชื่อ
และไม่มีการใส่ราคาของรายอื่น

## 7. ข้อจำกัดที่ทราบ

- benchmark สังเคราะห์เป็นเพียงตัวแทน ให้ยืนยันกับแอปของคุณเองด้วย `web load --scenario`
  และ `startup` ก่อนตัดสินใจขั้นสุดท้าย
- ใน VM มักมองไม่เห็นความเร็ว clock (ใน VM ARM64 ที่เราทดสอบ อ่านไม่ได้เลย) ผลจาก VM
  จึงแยกไม่ได้ว่าความต่างมาจาก clock หรือ IPC
- ผลจาก VM ใช้แทน CPU อีกรุ่นของผู้ขายเดียวกันไม่ได้ `cpubench info` แสดงเลข part
  MIDR ของ ARM ให้เทียบกับ CPU ที่คุณวางแผนจะซื้อ
