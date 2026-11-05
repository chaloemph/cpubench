package httpapi

import "html/template"

var indexTmpl = template.Must(template.New("index").Funcs(template.FuncMap{
	"ago": func(t interface{ Format(string) string }) string { return t.Format("15:04:05") },
}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>cpubench hub</title>
{{if .Active}}<meta http-equiv="refresh" content="5">{{end}}
<style>
:root{--bg:#fbfaf7;--card:#fff;--text:#1d1d1b;--muted:#6b6a65;--line:#e4e2da;--accent:#2b6cb0;--good:#2f7d32;--bad:#b3261e;--warn:#8a5a00}
@media (prefers-color-scheme:dark){:root:not([data-theme="light"]){--bg:#1b1b19;--card:#242422;--text:#ecebe6;--muted:#a3a29b;--line:#3a3935;--accent:#6aa5e8;--good:#7cc47f;--bad:#f2948c;--warn:#f0c46b}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 system-ui,sans-serif}main{max-width:1150px;margin:0 auto;padding:24px 16px 48px}
h1{font-size:22px;font-weight:600;margin:0 0 4px}h2{font-size:17px;font-weight:600;margin:28px 0 10px}
.scroll{overflow-x:auto;background:var(--card);border:1px solid var(--line);border-radius:10px}
table{border-collapse:collapse;width:100%;font-size:13px}th,td{padding:7px 10px;border-bottom:1px solid var(--line);text-align:left;white-space:nowrap;vertical-align:middle}
tr:last-child td{border-bottom:none}th{color:var(--muted);font-weight:600}.muted{color:var(--muted)}
button,select,input{font:inherit;font-size:13px}button{padding:5px 12px;border-radius:7px;border:1px solid var(--line);background:var(--card);color:var(--text);cursor:pointer}
button.primary{background:var(--accent);border-color:var(--accent);color:#fff}button:hover{border-color:var(--accent)}
select,input[type=text]{padding:5px 8px;border-radius:7px;border:1px solid var(--line);background:var(--card);color:var(--text)}
form.inline{display:inline}.bar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:10px 0}
.panel{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px 14px}
.on{color:var(--good)}.off{color:var(--muted)}.st-done{color:var(--good)}.st-failed{color:var(--bad)}.st-running{color:var(--accent)}.st-canceled,.st-waiting,.st-queued{color:var(--warn)}
code{font-size:12px;background:var(--line);padding:1px 5px;border-radius:4px}a{color:var(--accent)}
</style></head><body><main>
<h1>cpubench hub</h1>
<div class="muted">Connect a machine with <code>cpubench node --hub http://this-hub:8090{{if .Secured}} --token …{{end}}</code>, then press a button.{{if .Active}} This page refreshes every 5 s while tests run.{{end}}</div>

<h2>Machines</h2>
<div class="scroll"><table>
<tr><th>node</th><th>machine</th><th>status</th><th>arch</th><th>CPU</th><th>address</th><th>power</th><th>current job</th><th>run test</th></tr>
{{range .Nodes}}<tr>
<td><b>{{.Name}}</b></td><td>{{if .Machine}}{{.Machine}}{{else}}<span class="muted">{{.Name}}</span>{{end}}</td>
<td class="{{if .Online}}on{{else}}off{{end}}">{{if .Online}}online{{else}}offline{{end}}</td>
<td>{{.Arch}}</td><td>{{.CPU}}</td><td>{{.Addr}}:{{.WebPort}}</td><td>{{if .HasPower}}yes{{else}}<span class="muted">no</span>{{end}}</td>
<td>{{with .Job}}<span class="st-running">{{.Kind}} {{.Profile}}</span> · {{.Stage}}{{else}}<span class="muted">idle</span>{{end}}</td>
<td>{{if .Online}}{{$n := .Name}}{{range $.Profiles}}<form class="inline" method="post" action="/ui/run"><input type="hidden" name="node" value="{{$n}}"><input type="hidden" name="profile" value="{{.}}"><button {{if eq . "standard"}}class="primary"{{end}}>{{.}}</button></form> {{end}}{{end}}</td>
</tr>{{else}}<tr><td colspan="9" class="muted">No machines connected yet. On each server run: cpubench node --hub http://this-hub:8090{{if .Secured}} --token &lt;token&gt;{{end}}</td></tr>{{end}}
</table></div>

{{if .Nodes}}<div class="bar">
<form class="inline" method="post" action="/ui/run"><input type="hidden" name="node" value="*">
<select name="profile">{{range .Profiles}}<option {{if eq . "standard"}}selected{{end}}>{{.}}</option>{{end}}</select>
<button class="primary">Run on every online machine</button></form>
<a href="/compare?latest=1"><button type="button">Compare latest of every machine</button></a>
</div>

<h2>Web test</h2>
<div class="panel"><form method="post" action="/ui/web" class="bar" style="margin:0">
<label>Server <select name="sut">{{range .Nodes}}<option>{{.Name}}</option>{{end}}</select></label>
<label>Load generator <select name="loadgen">{{range .Nodes}}<option>{{.Name}}</option>{{end}}</select></label>
<select name="profile">{{range .Profiles}}<option {{if eq . "quick"}}selected{{end}}>{{.}}</option>{{end}}</select>
<button class="primary">Run web test</button>
<span class="muted">The server runs <code>web serve</code>; the load generator ramps until p99 breaks 100 ms. Use a separate machine as load generator.</span>
</form></div>{{end}}

{{if .Jobs}}<h2>Jobs</h2>
<div class="scroll"><table><tr><th>job</th><th>machine</th><th>type</th><th>profile</th><th>state</th><th>stage / message</th><th>updated</th><th>result</th><th></th></tr>
{{range .Jobs}}<tr><td class="muted">{{.ID}}</td><td>{{.Node}}</td><td>{{.Kind}}</td><td>{{.Profile}}</td>
<td class="st-{{.State}}">{{.State}}{{if .CancelRequested}} (stopping){{end}}</td><td>{{.Stage}}{{if .Message}} <span class="muted">{{.Message}}</span>{{end}}</td>
<td>{{ago .Updated}}</td><td>{{if .ResultID}}<a href="/compare?id={{.ResultID}}">report</a>{{end}}</td>
<td>{{if not .Finished}}<form class="inline" method="post" action="/ui/cancel"><input type="hidden" name="id" value="{{.ID}}"><button>Cancel</button></form>{{end}}</td></tr>{{end}}
</table></div>{{end}}

<h2>Results</h2>
<form action="/compare" method="get">
<div class="bar"><label>Baseline <input type="text" name="baseline" placeholder="label of the machine in production"></label>
<label><input type="radio" name="format" value="" checked> HTML</label><label><input type="radio" name="format" value="md"> Markdown</label>
<button type="submit">Compare selected</button></div>
<div class="scroll"><table><tr><th></th><th>label</th><th>machine</th><th>arch</th><th>CPU</th><th>mode</th><th>command</th><th>created (UTC)</th><th></th></tr>
{{range .Results}}<tr><td><input type="checkbox" name="id" value="{{.ID}}"></td><td>{{.Label}}</td><td>{{.Machine}}</td><td>{{.Arch}}</td><td>{{.CPU}}</td><td>{{.Mode}}</td><td>{{.Command}}</td><td>{{.Created.Format "2006-01-02 15:04"}}</td><td><a href="/api/results/{{.ID}}">json</a></td></tr>
{{else}}<tr><td colspan="9" class="muted">No results yet.</td></tr>{{end}}
</table></div></form></main></body></html>`))
