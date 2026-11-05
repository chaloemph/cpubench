# Security

[English](#english) · [ภาษาไทย](#ภาษาไทย)

## English

cpubench is a lab tool. Run the hub, the nodes and the web target on a test
network you control, never on the internet.

### Trust model

| Part | What it trusts | What to do |
|---|---|---|
| `hub` | Anyone who knows the token | Always start it with `--token` (or `$CPUBENCH_TOKEN`). Without a token anyone who can reach port 8090 can upload results and press the run buttons. |
| hub ↔ node | Plain HTTP, no TLS | The token travels in clear text (Bearer for the API, Basic auth for the buttons). Keep the hub on a private network or put it behind a TLS reverse proxy. |
| `node` | The hub it is pointed at | A node only runs the built-in job types (`auto` profiles, `web serve`, `web load`). It never runs shell commands from the hub, but a hub can tell it to send load to any URL. Point nodes only at your own hub. |
| BMC password | The machine it is set on | `$CPUBENCH_BMC_PASSWORD` stays on the node and is never sent to the hub. Keep it in a file with mode 600 (see `deploy/systemd/`). |
| `web serve` | Nobody | It is a benchmark target with no authentication. Stop it when the test ends. |
| `startup --cmd` | The local user | It runs the command you give it through `sh -c`, as the user who runs cpubench. |

Result files and reports contain hostnames, CPU models, BIOS versions, PCI
devices, disk models and BMC addresses. Read them before you share them, and use
`cpubench compare --for <label>` to make a copy for one vendor (see the
[README](README.en.md#a-copy-for-one-vendor)).

### Reporting a vulnerability

Please open a private report through GitHub's **Security → Report a
vulnerability** on this repository instead of a public issue.

## ภาษาไทย

cpubench เป็นเครื่องมือสำหรับห้องทดลอง ให้รัน hub, node และ web target ในเครือข่าย
ทดสอบที่คุณควบคุมเองเท่านั้น ห้ามเปิดบน internet

### โมเดลความเชื่อถือ

| ส่วน | เชื่อใคร | ควรทำอย่างไร |
|---|---|---|
| `hub` | ทุกคนที่รู้ token | เริ่มด้วย `--token` (หรือ `$CPUBENCH_TOKEN`) เสมอ ถ้าไม่มี token ใครก็ตามที่เข้าถึง port 8090 ได้จะอัปโหลดผลและกดปุ่มรันได้ |
| hub ↔ node | HTTP ธรรมดา ไม่มี TLS | token วิ่งเป็นข้อความธรรมดา (Bearer สำหรับ API, Basic auth สำหรับปุ่ม) ให้วาง hub ในเครือข่ายส่วนตัว หรือวาง reverse proxy ที่มี TLS ไว้หน้า hub |
| `node` | hub ที่ชี้ไป | node รันเฉพาะชนิดงานที่มีในตัว (profile ของ `auto`, `web serve`, `web load`) ไม่รันคำสั่ง shell จาก hub แต่ hub สั่งให้ส่งโหลดไปยัง URL ใดก็ได้ ดังนั้นให้ชี้ node ไปที่ hub ของคุณเองเท่านั้น |
| รหัสผ่าน BMC | เครื่องที่ตั้งค่าไว้ | `$CPUBENCH_BMC_PASSWORD` อยู่บน node และไม่ถูกส่งไป hub เก็บในไฟล์ที่สิทธิ์ 600 (ดู `deploy/systemd/`) |
| `web serve` | ไม่เชื่อใคร | เป็น benchmark target ที่ไม่มีการยืนยันตัวตน ให้หยุดเมื่อทดสอบเสร็จ |
| `startup --cmd` | ผู้ใช้ในเครื่อง | รันคำสั่งที่คุณให้ผ่าน `sh -c` ในฐานะผู้ใช้ที่รัน cpubench |

ไฟล์ผลและรายงานมีชื่อ host, รุ่น CPU, เวอร์ชัน BIOS, อุปกรณ์ PCI, รุ่นดิสก์ และที่อยู่ BMC
ให้อ่านก่อนแชร์ และใช้ `cpubench compare --for <label>` ทำสำเนาสำหรับผู้ขายรายเดียว
(ดู [README](README.md))

### แจ้งช่องโหว่

กรุณาแจ้งแบบส่วนตัวผ่าน **Security → Report a vulnerability** ของ GitHub ใน repository นี้
แทนการเปิด issue สาธารณะ
