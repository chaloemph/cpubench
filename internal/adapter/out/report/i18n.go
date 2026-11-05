package report

import "fmt"

// Languages a report can be written in. Thai is the default: the reader of
// these reports and every document beside them is Thai. Technical tokens
// (RPS, p99, SLO, vCPU, NUMA, RAM, labels, benchmark names) stay in English in
// both, as they do in the findings documents.
const (
	LangTH = "th"
	LangEN = "en"
)

type msg struct{ en, th string }

// normLang maps anything that is not English to the default, so a typo in
// --lang cannot yield a half-translated report.
func normLang(l string) string {
	if l == LangEN {
		return LangEN
	}
	return LangTH
}

// tfn looks a catalog key up in one language. Strings with arguments go
// through Sprintf; strings without are returned as they are, so a literal %
// in a plain message needs no escaping.
type tfn func(key string, args ...any) string

func translator(lang string) tfn {
	lang = normLang(lang)
	return func(key string, args ...any) string {
		m, ok := catalog[key]
		if !ok {
			return "!!" + key
		}
		s := m.th
		if lang == LangEN {
			s = m.en
		}
		if len(args) == 0 {
			return s
		}
		return fmt.Sprintf(s, args...)
	}
}

// T translates for the template, which calls it on the root value.
func (v view) T(key string, args ...any) string { return translator(v.Lang)(key, args...) }

var catalog = map[string]msg{
	// page
	"report.title":   {"cpubench report", "รายงาน cpubench"},
	"hdr.generated":  {"Generated %s", "สร้างเมื่อ %s"},
	"hdr.baseline":   {"baseline", "baseline"},
	"hdr.files":      {"%d result file(s)", "ไฟล์ผล %d ไฟล์"},
	"callout.title":  {"Read before any number below", "อ่านก่อนดูตัวเลขด้านล่าง"},
	"callout.notes":  {"Also worth knowing", "ควรรู้เพิ่มเติม"},
	"toc.title":      {"Contents", "สารบัญ"},
	"toc.first":      {"Read first", "อ่านก่อน"},
	"toc.later":      {"Open when in doubt", "เปิดดูเมื่อสงสัย"},
	"doc.comparison": {"comparison", "การเปรียบเทียบ"},

	// section titles
	"sec.conditions": {"Conditions", "เงื่อนไขการทดสอบ"},
	"sec.headline":   {"Headline", "สรุปตัวเลขหลัก"},
	"sec.category":   {"Change vs %s by category", "การเปลี่ยนแปลงเทียบกับ %s แยกตามหมวด"},
	"sec.layers":     {"Virtualisation / container overhead", "ต้นทุนของ virtualisation / container"},
	"sec.whole":      {"Whole machine vs %s", "ทั้งเครื่องเทียบกับ %s"},
	"sec.web":        {"Web load test", "ทดสอบ web ด้วยโหลด"},
	"sec.tco":        {"On-prem TCO for %s RPS (N+%d)", "TCO แบบ on-prem สำหรับ %s RPS (N+%d)"},
	"sec.scaling":    {"Thread scaling", "การขยายตามจำนวน thread"},
	"sec.latency":    {"Memory hierarchy", "ลำดับชั้นของหน่วยความจำ"},
	"sec.stress":     {"Stress (sustained vs peak)", "Stress (ค่าต่อเนื่องเทียบกับค่าสูงสุด)"},
	"sec.power":      {"Power curve", "เส้นโค้งกำลังไฟ"},
	"sec.density":    {"Density (instances per machine)", "Density (จำนวน instance ต่อเครื่อง)"},
	"sec.startup":    {"Startup and warmup", "เวลาเริ่มต้นและ warmup"},
	"sec.benchmarks": {"All benchmarks", "ผล benchmark ทั้งหมด"},
	"sec.doctor":     {"Doctor findings", "ผลตรวจสภาพแวดล้อม (doctor)"},
	"sec.limits":     {"What this report does not answer", "สิ่งที่รายงานนี้ตอบไม่ได้"},
	"sec.terms":      {"Terms used in this report", "ความหมายของคำที่ใช้ในรายงาน"},

	// one line on why each section is worth opening
	"hint.conditions": {"check the columns match before reading any ratio", "ตรวจว่าทุกคอลัมน์ตรงกันก่อนอ่านอัตราส่วนใดๆ"},
	"hint.headline":   {"the top-line figures", "ตัวเลขหลักโดยสรุป"},
	"hint.category":   {"the answer: one row per category, and what to distrust in it", "คำตอบหลัก: หนึ่งแถวต่อหมวด พร้อมบอกว่าแถวไหนไม่ควรเชื่อ"},
	"hint.layers":     {"what the layer (VM, container, pod) costs on the same machine", "ต้นทุนของ layer (VM, container, pod) บนเครื่องเดียวกัน"},
	"hint.whole":      {"each machine at its own highest thread count, then per core, socket, watt and price", "แต่ละเครื่องที่จำนวน thread สูงสุดของตัวเอง แล้วคิดต่อ core, socket, วัตต์ และราคา"},
	"hint.web":        {"max RPS at the SLO, and which side saturated first", "max RPS ที่ SLO และฝั่งไหนเต็มก่อน"},
	"hint.tco":        {"servers and cost for the target load", "จำนวนเครื่องและต้นทุนสำหรับโหลดเป้าหมาย"},
	"hint.limits":     {"read before reusing the numbers anywhere else", "อ่านก่อนนำตัวเลขไปใช้ที่อื่น"},
	"hint.stress":     {"does the speed hold over the run; errors should be 0", "ความเร็วคงที่ตลอดการรันหรือไม่ และ error ควรเป็น 0"},
	"hint.benchmarks": {"every measurement, folded; open it to see which row drives a category", "ผลวัดทุกแถว พับไว้ เปิดดูเพื่อหาว่าแถวไหนทำให้หมวดนั้นเปลี่ยน"},
	"hint.doctor":     {"environment checks on each machine; folded unless one warns", "ผลตรวจสภาพแวดล้อมของแต่ละเครื่อง พับไว้เว้นแต่มีรายการที่เตือน"},
	"hint.terms":      {"what the words in the labels mean", "ความหมายของคำที่ใช้ใน label"},

	// verdict
	"verdict.single": {"Single %s run of %s on %s; no baseline in this report to compare against.",
		"รันครั้งเดียว (%s) ของ %s บน %s รายงานนี้ไม่มี baseline ให้เปรียบเทียบ"},
	"verdict.both":  {"%s is %s against %s over the micro suite and %s on max RPS @ SLO", "%s อยู่ที่ %s เทียบกับ %s ในชุด micro benchmark และ %s ใน max RPS @ SLO"},
	"verdict.micro": {"%s is %s against %s over the micro suite", "%s อยู่ที่ %s เทียบกับ %s ในชุด micro benchmark"},
	"verdict.web":   {"%s is %s against %s on max RPS @ SLO", "%s อยู่ที่ %s เทียบกับ %s ใน max RPS @ SLO"},
	"scope.differs": {" Columns were given different CPU counts, so read the conditions table before any ratio.",
		" แต่ละคอลัมน์ได้ CPU ไม่เท่ากัน ให้ดูตารางเงื่อนไขก่อนอ่านอัตราส่วนใดๆ"},
	"scope.per": {" Per %d effective CPUs: neither per machine nor per watt.", " เป็นค่าต่อ %d effective CPU ไม่ใช่ต่อเครื่อง และไม่ใช่ต่อวัตต์"},

	// caveats derived from the data
	"cav.checksum_hard": {"%s produced different checksums across columns outside the float kernels, so the columns did not run the same work and their ratios are void. Check that every column ran the same profile and build.",
		"%s ให้ checksum ต่างกันระหว่างคอลัมน์ในหมวดที่ไม่ใช่ float แปลว่าแต่ละคอลัมน์ไม่ได้รันงานเดียวกัน อัตราส่วนจึงใช้ไม่ได้ ให้ตรวจว่าทุกคอลัมน์รัน profile และ build เดียวกัน"},
	"cav.checksum_soft": {"%s differ in checksum between architectures, which is expected where arm64 fuses multiply-add; the throughput is still comparable.",
		"%s มี checksum ต่างกันระหว่างสถาปัตยกรรม ซึ่งเป็นเรื่องปกติเพราะ arm64 รวม multiply-add เป็นคำสั่งเดียว throughput ยังเทียบกันได้"},
	"cav.backend": {"%s ran different implementations per column: the ratio shows which instructions each CPU exposes, not how fast the CPUs are.",
		"%s รัน implementation ต่างกันในแต่ละคอลัมน์ อัตราส่วนจึงบอกว่า CPU แต่ละตัวมีชุดคำสั่งอะไร ไม่ได้บอกว่า CPU เร็วแค่ไหน"},
	"cav.context": {"Columns ran in different contexts (%s): part of any difference is the layer, not the CPU.",
		"แต่ละคอลัมน์รันในบริบทต่างกัน (%s) ส่วนต่างบางส่วนมาจาก layer ไม่ใช่ CPU"},
	"cav.baseline_guess": {"The baseline %s was picked automatically as the first of several machines (%s), and every ratio is against it. Pass --baseline with the machine in production now.",
		"baseline %s ถูกเลือกอัตโนมัติเป็นเครื่องแรกจากหลายเครื่อง (%s) และทุกอัตราส่วนเทียบกับเครื่องนี้ ให้ระบุ --baseline เป็นเครื่องที่ใช้งานจริงอยู่ตอนนี้"},
	"cav.shared": {"This copy was prepared to share and shows only some of the machines tested.",
		"สำเนานี้จัดทำไว้สำหรับส่งต่อ จึงแสดงเพียงบางเครื่องที่ทดสอบ"},
	"cav.anon": {"%s are other machines, shown without their names, CPU model, platform, devices or prices.",
		"%s เป็นเครื่องอื่นที่ซ่อนชื่อ รุ่น CPU แพลตฟอร์ม อุปกรณ์ และราคาไว้"},
	"cav.cpus": {"Columns had different effective CPU counts (%s): the ratios are not like-for-like.",
		"แต่ละคอลัมน์มี effective CPU ไม่เท่ากัน (%s) อัตราส่วนจึงเทียบกันตรงๆ ไม่ได้"},
	"cav.unpinned": {"%s ran without NUMA pinning: the memory (RAM) and concurrency rows vary from run to run, so do not compare them with another machine. Integer, float and crypto rows still hold. See the terms at the end of this report.",
		"%s รันโดยไม่ได้ผูก NUMA แถวหมวด memory (RAM) และ concurrency จะแกว่งไปมาระหว่างรอบ จึงห้ามเทียบกับเครื่องอื่น ส่วนแถว integer, float และ crypto ยังเชื่อได้ ดูความหมายของคำท้ายรายงาน"},
	"cav.unpinned_mixed": {" Pinned and unpinned columns are both here, so any difference between them in those rows comes from the NUMA setting, not the CPU.",
		" รายงานนี้มีทั้งคอลัมน์ที่ผูกและไม่ได้ผูก NUMA ส่วนต่างระหว่างสองกลุ่มในแถวเหล่านั้นจึงมาจากการตั้งค่า NUMA ไม่ใช่ CPU"},
	"cav.arch_mixed": {"Columns of the same architecture were built at different instruction baselines (%s).",
		"คอลัมน์ของสถาปัตยกรรมเดียวกันถูก build ที่ instruction baseline ต่างกัน (%s)"},
	"cav.arch_base": {"Every column is built at the lowest baseline of their architecture (%s), so no binary here uses the newer vector or crypto instructions its CPU may have. That is symmetric between columns, but it is not either CPU's ceiling.",
		"ทุกคอลัมน์ถูก build ที่ baseline ต่ำสุดของสถาปัตยกรรมตัวเอง (%s) จึงไม่มี binary ไหนใช้ชุดคำสั่ง vector หรือ crypto ใหม่ๆ ที่ CPU อาจมี เป็นเงื่อนไขที่เท่ากันทุกคอลัมน์ แต่ไม่ใช่ศักยภาพสูงสุดของ CPU ตัวไหนเลย"},
	"cav.once": {"%s measured every benchmark only once (1 iteration), so its figures have no spread and noise cannot be told from a real difference. Use at least 3 iterations (every profile uses 2 to 5).",
		"%s วัดทุก benchmark เพียงครั้งเดียว (1 iteration) ตัวเลขจึงไม่มีช่วงแกว่ง และแยกไม่ได้ว่าความต่างมาจากสัญญาณรบกวนหรือของจริง ควรใช้อย่างน้อย 3 iteration (ทุก profile ใช้ 2 ถึง 5)"},
	"cav.repeats": {"%s combines %d runs: each row is the median of the runs' medians, and its CV is the spread between runs. Stress, power, web and density come from the first run.",
		"%s รวม %d รอบ: แต่ละแถวคือ median ของ median แต่ละรอบ และ CV คือความแกว่งระหว่างรอบ ส่วน stress, กำลังไฟ, web และ density มาจากรอบแรก"},
	"cav.throttled": {"%s was CPU-throttled by its cgroup %d times (%.2fs total).", "%s ถูก cgroup จำกัด CPU (throttle) %d ครั้ง รวม %.2f วินาที"},
	"cav.loadgen": {"%s: at %s rps the server used only %.0f%% of its CPUs while the step failed the SLO. That number is the load generator's ceiling, not the server's; do not compare it.",
		"%s: ที่ %s rps server ใช้ CPU เพียง %.0f%% ทั้งที่ขั้นนั้นไม่ผ่าน SLO ตัวเลขนี้เป็นเพดานของตัวยิงโหลด ไม่ใช่ของ server ห้ามนำไปเทียบ"},
	"cav.rows_more": {"%s and %d more rows", "%s และอีก %d แถว"},

	// what the report cannot answer
	"lim.ratios": {"Ratios are per the effective CPU count in the conditions table. Per-machine and per-watt comparisons need runs that fill each machine, plus a power reading.",
		"อัตราส่วนเป็นค่าต่อจำนวน effective CPU ในตารางเงื่อนไข การเทียบต่อเครื่องและต่อวัตต์ต้องรันให้เต็มเครื่องและอ่านค่าไฟด้วย"},
	"lim.nopower": {"No power was recorded, so there is no efficiency or TCO figure here (`--power redfish://…`).",
		"ไม่ได้บันทึกค่าไฟ จึงไม่มีตัวเลขประสิทธิภาพต่อวัตต์หรือ TCO (ใช้ `--power redfish://…`)"},
	"lim.noweb": {"No web result: CPU kernels alone do not show what the machine serves at an SLO.",
		"ไม่มีผล web: ผลจาก CPU kernel อย่างเดียวไม่บอกว่าเครื่องรับโหลดได้เท่าไรที่ SLO"},
	"lim.loopback": {"The web test was driven from the machine under test, so it measures only the CPUs left to the server and excludes NIC, virtio and IRQ cost.",
		"ทดสอบ web โดยยิงจากเครื่องที่ถูกทดสอบเอง จึงวัดได้เฉพาะ CPU ที่เหลือให้ server และไม่รวมต้นทุนของ NIC, virtio และ IRQ"},
	"lim.layer": {"Every column ran in %s: nothing here says what the same CPUs do on bare metal.",
		"ทุกคอลัมน์รันใน %s: ผลนี้ไม่ได้บอกว่า CPU ชุดเดียวกันทำงานบน bare metal ได้เท่าไร"},
	"lim.models": {"The columns are different CPU models on different hosts, so host configuration is part of every difference.",
		"แต่ละคอลัมน์เป็น CPU คนละรุ่นบนคนละ host การตั้งค่าของ host จึงเป็นส่วนหนึ่งของทุกส่วนต่าง"},

	// notes beside a category ratio
	"note.checksum": {"checksum mismatch", "checksum ไม่ตรง"},
	"note.impl":     {"implementation differs", "implementation ต่างกัน"},
	"note.noisy":    {"noisy: %d of %d measurements over %.0f%% CV", "แกว่ง: %d จาก %d ค่าวัดเกิน %.0f%% CV"},

	// terms
	"pin.name": {"pinned / unpinned (NUMA pinning)", "pinned / unpinned (การผูก NUMA)"},
	"pin.intro": {"A host with several NUMA nodes (CPU sockets or dies, each with its own RAM modules) serves a thread fastest from the RAM attached to the node it runs on. RAM on another node costs more, and across sockets much more. Pinning fixes the distance between a VM's CPUs and its RAM. This is about main memory (RAM, DRAM), not the CPU's own caches (L1 to L3), which NUMA does not move.",
		"host ที่มีหลาย NUMA node (socket หรือ die ของ CPU ที่แต่ละตัวมีแถว RAM ของตัวเอง) จะให้ thread เร็วที่สุดเมื่อใช้ RAM ที่ต่อกับ node ที่ thread นั้นรันอยู่ RAM ของ node อื่นช้ากว่า และถ้าข้าม socket จะช้ากว่ามาก การผูก (pin) คือการกำหนดระยะระหว่าง CPU ของ VM กับ RAM ของมัน เรื่องนี้เกี่ยวกับหน่วยความจำหลัก (RAM, DRAM) ไม่ใช่ cache ในตัว CPU (L1 ถึง L3) ซึ่ง NUMA ไม่ได้ย้าย"},
	"pin.p1": {"pinned: the VM's vCPUs are tied to the CPUs of one host node (CPU affinity) and its RAM is allocated only on that node (bind policy). Every vCPU stays next to every page of RAM it uses.",
		"pinned: vCPU ของ VM ถูกผูกกับ CPU ของ node เดียวบน host (CPU affinity) และ RAM ถูกจัดเฉพาะบน node นั้น (bind policy) ทุก vCPU จึงอยู่ใกล้ทุกหน้า RAM ที่ใช้"},
	"pin.p2": {"unpinned: neither is tied. The host may move a vCPU to another node or socket at any moment, and the VM's RAM may be spread over several nodes, so the distance between a running vCPU and a page of RAM keeps changing.",
		"unpinned: ไม่ได้ผูกทั้งสองอย่าง host ย้าย vCPU ไป node หรือ socket อื่นได้ทุกเมื่อ และ RAM ของ VM อาจกระจายหลาย node ระยะระหว่าง vCPU ที่กำลังรันกับหน้า RAM จึงเปลี่ยนไปเรื่อยๆ"},
	"pin.p3": {"Effect: unpinned runs differ from run to run, mostly in the memory rows (RAM latency and bandwidth) and the concurrency rows (atomics, locks, which also touch RAM), and one VM can score several times apart from one run to the next. Pinned runs repeat. Integer, float and crypto rows held up either way.",
		"ผลที่ตามมา: การรันแบบ unpinned ต่างกันไปทุกรอบ โดยเฉพาะแถวหมวด memory (latency และ bandwidth ของ RAM) และ concurrency (atomic, lock ซึ่งแตะ RAM ด้วย) VM ตัวเดียวกันอาจได้คะแนนต่างกันหลายเท่าระหว่างรอบ ส่วนแบบ pinned ซ้ำได้ แถว integer, float และ crypto เชื่อได้ทั้งสองแบบ"},
	"pin.p4": {"Pinning can lower peak RAM bandwidth, probably because RAM spread over several nodes uses several memory controllers. The pinned figure is the one that matches a VM configured this way in service.",
		"การผูกอาจทำให้ bandwidth สูงสุดของ RAM ลดลง คาดว่าเพราะ RAM ที่กระจายหลาย node ใช้ memory controller หลายตัว ตัวเลขแบบ pinned คือตัวที่ตรงกับ VM ที่ตั้งค่าแบบนี้ตอนใช้งานจริง"},
	"pin.p5": {"The guest cannot tell: it reports one NUMA node in both cases, so the label is the only record. Check on the host with `numastat -p <qemu pid>` (the VM's RAM on one node), and in the guest with `archprobe -mode pages` (no slow pages when pinned).",
		"ใน guest มองไม่เห็นความต่าง: รายงาน NUMA node เดียวทั้งสองกรณี label จึงเป็นเพียงบันทึกเดียว ตรวจที่ host ด้วย `numastat -p <qemu pid>` (RAM ของ VM อยู่ node เดียว) และใน guest ด้วย `archprobe -mode pages` (ผูกแล้วต้องไม่มีหน้าช้า)"},
	"pin.p6": {"Compare pinned with pinned. A column labelled unpinned shows what the host happened to do during that run.",
		"เทียบ pinned กับ pinned เท่านั้น คอลัมน์ที่ label เป็น unpinned แสดงสิ่งที่ host ทำในรอบนั้นโดยบังเอิญ"},

	// headline cards
	"card.cpu":         {"CPU", "CPU"},
	"card.context":     {"Context", "บริบท"},
	"card.benchmarks":  {"Benchmarks", "Benchmark"},
	"card.logical":     {"%s · %d logical CPUs", "%s · %d logical CPU"},
	"card.effective":   {"%d effective CPUs", "%d effective CPU"},
	"card.findings":    {"%d doctor findings", "ผลตรวจ doctor %d รายการ"},
	"card.maxrps":      {"Max RPS @ SLO", "Max RPS @ SLO"},
	"card.micro":       {"Micro suite (geomean)", "Micro suite (geomean)"},
	"card.whole":       {"Whole machine (geomean)", "ทั้งเครื่อง (geomean)"},
	"card.whole_sub":   {"%s vs %s · %d vs %d threads", "%s เทียบกับ %s · %d เทียบกับ %d thread"},
	"card.p99low":      {"p99 at low load", "p99 ตอนโหลดต่ำ"},
	"card.p99low_sub":  {"latency, lower is better", "latency ยิ่งต่ำยิ่งดี"},
	"card.vs":          {"%s vs %s", "%s เทียบกับ %s"},
	"card.servers":     {"Servers for %s RPS", "จำนวนเครื่องสำหรับ %s RPS"},
	"card.servers_sub": {"%s · %.0f-yr TCO %s %s", "%s · TCO %.0f ปี %s %s"},

	// conditions table rows
	"env.machine":   {"Machine / mode", "เครื่อง / โหมด"},
	"env.cpu":       {"CPU", "CPU"},
	"env.identity":  {"CPU identity", "ตัวตนของ CPU"},
	"env.arch":      {"Arch", "สถาปัตยกรรม"},
	"env.topology":  {"Sockets × cores × threads", "Socket × core × thread"},
	"env.eff":       {"Effective CPUs", "Effective CPU"},
	"env.mhz":       {"Max MHz", "Max MHz"},
	"env.numa":      {"NUMA nodes", "NUMA node"},
	"env.page":      {"Page size", "ขนาด page"},
	"env.memory":    {"Memory", "หน่วยความจำ"},
	"env.memory_v":  {"%d GB · %d DIMMs", "%d GB · %d DIMM"},
	"env.platform":  {"Platform", "แพลตฟอร์ม"},
	"env.bios":      {"BIOS", "BIOS"},
	"env.dimms":     {"DIMMs", "DIMM"},
	"env.dimms_v":   {"%d of %d slots · %s", "ใส่ %d จาก %d ช่อง · %s"},
	"env.rated":     {"%s (rated %s)", "%s (สเปก %s)"},
	"env.storage":   {"Storage controllers", "Storage controller"},
	"env.disks":     {"Disks", "Disk"},
	"env.netctl":    {"Network controllers", "Network controller"},
	"env.os":        {"OS / kernel", "OS / kernel"},
	"env.gov":       {"Governor / boost", "Governor / boost"},
	"env.mitig":     {"Mitigations", "Mitigations"},
	"env.loadgen":   {"Web load generator", "ตัวยิงโหลด web"},
	"env.loadgen_v": {"%s · %s · %d CPUs · %d agent(s)", "%s · %s · %d CPU · %d agent"},
	"env.power":     {"Power (avg / max)", "กำลังไฟ (เฉลี่ย / สูงสุด)"},
	"env.build":     {"Build", "Build"},
	"env.run":       {"Run", "การรัน"},
	"env.runs":      {"Repeated runs", "รอบที่รวม"},
	"env.runs_v":    {"%d: %s", "%d รอบ: %s"},

	// chart titles and axes
	"chart.scaling":     {"%s — thread scaling", "%s — การขยายตามจำนวน thread"},
	"chart.threads":     {"threads", "thread"},
	"chart.latency":     {"Memory latency ladder (L1 → L2 → L3 → DRAM)", "บันไดความหน่วงของหน่วยความจำ (L1 → L2 → L3 → DRAM)"},
	"chart.workingset":  {"working set", "working set"},
	"chart.nsload":      {"ns per load", "ns ต่อการอ่าน"},
	"chart.stress_rate": {"Throughput over time (% of peak)", "Throughput ตามเวลา (% ของค่าสูงสุด)"},
	"chart.stress_mhz":  {"Average CPU frequency", "ความถี่ CPU เฉลี่ย"},
	"chart.seconds":     {"seconds", "วินาที"},
	"chart.pctpeak":     {"% of peak", "% ของค่าสูงสุด"},
	"chart.power":       {"Power vs utilisation", "กำลังไฟเทียบกับการใช้งาน CPU"},
	"chart.util":        {"CPU utilisation %", "การใช้งาน CPU %"},
	"chart.watts":       {"watts", "วัตต์"},
	"chart.density":     {"Per-instance throughput as instances are added", "throughput ต่อ instance เมื่อเพิ่มจำนวน instance"},
	"chart.instances":   {"instances", "instance"},
	"chart.pctsolo":     {"% of solo", "% ของตอนรันเดี่ยว"},
	"chart.web_lat":     {"p99 latency vs achieved load", "latency p99 เทียบกับโหลดที่ทำได้"},
	"chart.web_tl":      {"p99 per second (soak / spike timeline)", "p99 ต่อวินาที (ไทม์ไลน์ soak / spike)"},
	"chart.rps":         {"requests/s", "request/วินาที"},
	"chart.speedup":     {"Speedup vs baseline by category", "ความเร็วเทียบกับ baseline แยกตามหมวด"},
	"chart.baseline":    {"= baseline", "= baseline"},
	"chart.slower":      {"slower", "ช้ากว่า"},
	"chart.faster":      {"faster", "เร็วกว่า"},

	// tables
	"tbl.category":   {"category", "หมวด"},
	"tbl.care":       {"read with care", "ควรระวัง"},
	"whole.machine":  {"Per machine", "ต่อเครื่อง"},
	"whole.core":     {"Per core", "ต่อ core"},
	"whole.socket":   {"Per socket", "ต่อ socket"},
	"whole.watt":     {"Per watt (full load)", "ต่อวัตต์ (โหลดเต็ม)"},
	"whole.price":    {"Per server price", "ต่อราคาเครื่อง"},
	"whole.threads":  {"Threads at the top step", "จำนวน thread ขั้นสูงสุด"},
	"whole.cores":    {"Usable cores", "core ที่ใช้ได้"},
	"whole.sockets":  {"Sockets (bare metal only)", "Socket (เฉพาะ bare metal)"},
	"whole.watts":    {"Watts at full load", "วัตต์ตอนโหลดเต็ม"},
	"whole.price_v":  {"Server price", "ราคาเครื่อง"},
	"whole.n":        {"Workloads behind the ratio", "จำนวน workload ที่ใช้คำนวณ"},
	"whole.divisors": {"What each machine is divided by", "ตัวหารของแต่ละเครื่อง"},
	"whole.note": {"Each column runs at its own highest thread count, so machines with different core counts are compared as machines. Each ratio is the geomean over the multi-threaded throughput workloads both sides ran; sync/atomic-contended and sync/mutex are left out because their total does not grow with the machine. The category table compares equal thread counts instead, which is per core. Watts are the whole server's power during stress or at the power curve's full-load step, not during each workload. Sockets count only runs that had the whole physical machine, and price comes from the TCO model.",
		"แต่ละคอลัมน์รันที่จำนวน thread สูงสุดของตัวเอง เครื่องที่มี core ไม่เท่ากันจึงถูกเทียบกันแบบทั้งเครื่อง แต่ละอัตราส่วนคือ geomean ของ workload แบบ throughput ที่ใช้หลาย thread และรันทั้งสองฝั่ง ไม่รวม sync/atomic-contended และ sync/mutex เพราะผลรวมของสองงานนี้ไม่เพิ่มตามขนาดเครื่อง ตารางแยกตามหมวดด้านบนเทียบที่จำนวน thread เท่ากัน ซึ่งก็คือต่อ core ค่าวัตต์เป็นกำลังไฟของทั้งเครื่องระหว่าง stress หรือที่ขั้นโหลดเต็มของ power curve ไม่ใช่ระหว่างแต่ละ workload ส่วน socket นับเฉพาะ run ที่ได้ทั้งเครื่องจริง และราคามาจาก TCO model"},
	"scope.whole":     {" Per %d effective CPUs; the whole-machine section compares the machines at full size.", " เป็นค่าต่อ %d effective CPU ส่วนการเทียบแบบทั้งเครื่องอยู่ในหัวข้อทั้งเครื่อง"},
	"tbl.geomean":     {"Geometric mean of every benchmark and thread count in the category, with the lowest and highest row in brackets. Latency results are inverted, so a positive number (or a bar to the right) is always better. A category mixes units (MB/s, ns, ops/s) and so has no measured value of its own: open a category for the measured values of every machine, row by row.", "ค่าเฉลี่ยเรขาคณิต (geomean) ของทุก benchmark และทุกจำนวน thread ในหมวด ในวงเล็บคือแถวที่ต่ำสุดและสูงสุดของหมวด ค่า latency ถูกกลับด้านไว้แล้ว ตัวเลขบวก (หรือแท่งที่ไปทางขวา) จึงดีกว่าเสมอ หมวดหนึ่งรวมหลายหน่วย (MB/s, ns, ops/s) จึงไม่มีค่าจริงของหมวด คลิกชื่อหมวดเพื่อดูค่าที่วัดได้จริงของทุกเครื่องทีละแถว"},
	"col.run":         {"run", "การรัน"},
	"col.errors":      {"errors", "error"},
	"lay.machine":     {"machine", "เครื่อง"},
	"lay.cmp":         {"compared with", "เทียบกับ"},
	"lay.micro":       {"micro suite", "micro suite"},
	"lay.web":         {"web max RPS", "web max RPS"},
	"web.target":      {"target rps", "เป้าหมาย (rps)"},
	"web.achieved":    {"achieved", "ทำได้จริง"},
	"web.dropped":     {"dropped", "ตกหล่น"},
	"web.sut":         {"SUT CPU", "CPU ของ SUT"},
	"web.watts":       {"watts", "วัตต์"},
	"web.pass":        {"pass", "ผ่าน"},
	"web.fail":        {"fail", "ไม่ผ่าน"},
	"web.knee":        {"Max RPS @ SLO (p99 ≤ %.0f ms)", "Max RPS @ SLO (p99 ≤ %.0f ms)"},
	"web.maxrps":      {"max RPS @ SLO", "max RPS @ SLO"},
	"web.recovery":    {"recovery %s s", "กู้คืนใน %s วินาที"},
	"web.ceiling":     {"A step that failed the SLO while SUT CPU was still low means the load generator ran out first, not the server.", "ขั้นที่ไม่ผ่าน SLO ทั้งที่ CPU ของ SUT ยังเหลือ แปลว่าตัวยิงโหลดเต็มก่อน ไม่ใช่ server"},
	"tco.server":      {"server", "เครื่อง"},
	"tco.maxrps":      {"max RPS @ SLO", "max RPS @ SLO"},
	"tco.watts":       {"watts", "วัตต์"},
	"tco.rpspw":       {"RPS / watt", "RPS / วัตต์"},
	"tco.servers":     {"servers", "จำนวนเครื่อง"},
	"tco.capex":       {"capex", "ค่าเครื่อง (capex)"},
	"tco.energy":      {"energy / year", "ค่าไฟต่อปี"},
	"tco.total":       {"total", "รวม"},
	"tco.note":        {"note", "หมายเหตุ"},
	"unit.year":       {"y", "ปี"},
	"str.threads":     {"threads", "thread"},
	"str.duration":    {"duration", "ระยะเวลา"},
	"str.peak":        {"peak it/s", "it/s สูงสุด"},
	"str.sustained":   {"sustained it/s", "it/s ต่อเนื่อง"},
	"str.change":      {"change", "เปลี่ยนแปลง"},
	"str.mhz":         {"MHz min–max", "MHz ต่ำสุด–สูงสุด"},
	"str.temp":        {"max °C", "°C สูงสุด"},
	"str.watts":       {"avg W", "W เฉลี่ย"},
	"str.throttled":   {"throttled", "ถูก throttle"},
	"str.kernel":      {"kernel", "kernel"},
	"den.workload":    {"workload", "workload"},
	"den.threads":     {"threads / instance", "thread ต่อ instance"},
	"den.max":         {"max instances ≥90% of solo", "instance สูงสุดที่ยังได้ ≥90% ของตอนรันเดี่ยว"},
	"sta.command":     {"command", "คำสั่ง"},
	"sta.ready":       {"ready (median)", "พร้อมใช้งาน (median)"},
	"sta.warmup":      {"warmup until stable", "warmup จนนิ่ง"},
	"numa.title":      {"NUMA latency — %s (rows: CPU node, columns: memory node)", "latency ของ NUMA — %s (แถว: node ของ CPU, คอลัมน์: node ของ memory)"},
	"numa.corner":     {"cpu \\ mem", "cpu \\ mem"},
	"numa.node":       {"node %d", "node %d"},
	"bm.name":         {"benchmark", "benchmark"},
	"bm.threads":      {"threads", "thread"},
	"bm.unit":         {"unit", "หน่วย"},
	"bm.vsbase":       {"%s vs base", "%s เทียบกับ base"},
	"bm.show":         {"Show %d rows", "แสดง %d แถว"},
	"bm.base":         {"%s (baseline)", "%s (baseline)"},
	"bm.checksum":     {"checksum differs", "checksum ต่างกัน"},
	"bm.checksum_tip": {"results differ between machines", "ผลต่างกันระหว่างเครื่อง"},
	"bm.impl":         {"implementation differs", "implementation ต่างกัน"},
	"bm.impl_tip":     {"machines ran different implementations (dedicated instructions vs other code)", "แต่ละเครื่องรัน implementation ต่างกัน (ชุดคำสั่งเฉพาะกับโค้ดอื่น)"},
	"bm.md_checksum":  {" ⚠ checksum", " ⚠ checksum ต่างกัน"},
	"bm.md_impl":      {" ⚠ implementation differs", " ⚠ implementation ต่างกัน"},
	"bm.note": {"Value is the median of the measured iterations; ± is the coefficient of variation (amber when above 5%). A checksum difference on float kernels can come from fused multiply-add on arm64; on integer kernels it means the result really differs. Crypto rows show the implementation each CPU ran: hardware = dedicated instructions, simd/asm = other optimised code, software = generic Go.",
		"ค่าคือ median ของรอบที่วัด ส่วน ± คือ coefficient of variation (สีเหลืองเมื่อเกิน 5%) checksum ที่ต่างกันในงาน float อาจมาจาก fused multiply-add บน arm64 แต่ในงาน integer หมายความว่าผลต่างกันจริง แถว crypto แสดง implementation ที่ CPU แต่ละตัวรัน: hardware = ชุดคำสั่งเฉพาะ, simd/asm = โค้ดที่ optimise แบบอื่น, software = Go ทั่วไป"},
	"doc.show": {"Show %d findings", "แสดง %d รายการ"},
}
