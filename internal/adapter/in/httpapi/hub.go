package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/control"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

// Hub collects result files from many machines, renders comparisons and
// dispatches tests to connected nodes (`cpubench node`) from buttons.
type Hub struct {
	Store    port.ResultStore
	HTML     port.ReportRenderer
	Markdown port.ReportRenderer
	// Token, when set, is required (Bearer) to upload results and poll for
	// jobs, and as the Basic-auth password for the Run buttons.
	Token string
	// PollWait is how long a node's poll is held open when there is no work.
	PollWait time.Duration

	once  sync.Once
	mu    sync.Mutex
	board *control.Board
	wake  map[string]chan struct{}
}

type summary struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Machine string    `json:"machine"`
	Arch    string    `json:"arch"`
	CPU     string    `json:"cpu"`
	Mode    string    `json:"mode"`
	Command string    `json:"command"`
	Created time.Time `json:"created_at"`
}

func summarize(f result.File) summary {
	return summary{ID: f.ID, Label: f.Label, Machine: f.Machine, Arch: f.System.CPU.Arch, CPU: f.System.CPU.Model,
		Mode: string(f.System.Context.Mode), Command: f.Command, Created: f.CreatedAt}
}

func (h *Hub) init() {
	h.once.Do(func() {
		h.board = control.NewBoard()
		h.wake = map[string]chan struct{}{}
		if h.PollWait == 0 {
			h.PollWait = 20 * time.Second
		}
	})
}

func (h *Hub) Handler() http.Handler {
	h.init()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/results", h.api(h.upload))
	mux.HandleFunc("GET /api/results", h.list)
	mux.HandleFunc("GET /api/results/{id}", h.get)
	mux.HandleFunc("POST /api/nodes/poll", h.api(h.poll))
	mux.HandleFunc("POST /api/jobs/{id}/status", h.api(h.status))
	mux.HandleFunc("GET /api/jobs", h.jobs)
	mux.HandleFunc("POST /ui/run", h.ui(h.runAuto))
	mux.HandleFunc("POST /ui/web", h.ui(h.runWeb))
	mux.HandleFunc("POST /ui/cancel", h.ui(h.cancel))
	mux.HandleFunc("GET /compare", h.compare)
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return mux
}

// api requires the Bearer token for machine-to-hub calls.
func (h *Hub) api(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) != 1 {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

// ui protects the buttons: Basic auth with the token as password, and a
// same-origin check so another site cannot submit the forms (CSRF).
func (h *Hub) ui(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		if h.Token != "" {
			_, pass, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(h.Token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="cpubench hub (password = hub token)"`)
				http.Error(w, "hub token required", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func sameOrigin(r *http.Request) bool {
	for _, hdr := range []string{"Origin", "Referer"} {
		v := r.Header.Get(hdr)
		if v == "" || v == "null" {
			continue
		}
		u, err := url.Parse(v)
		return err == nil && u.Host == r.Host
	}
	return true // non-browser client
}

func newID(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + "-" + time.Now().UTC().Format("0102-150405") + "-" + hex.EncodeToString(b)
}

func (h *Hub) notify(nodes ...string) {
	for _, n := range nodes {
		if ch, ok := h.wake[n]; ok {
			close(ch)
			delete(h.wake, n)
		}
	}
}

// ---- results ----

func (h *Hub) upload(w http.ResponseWriter, r *http.Request) {
	var f result.File
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&f); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(f.Schema, "cpubench/") || f.ID == "" || strings.ContainsAny(f.ID, `/\.`) {
		http.Error(w, "not a cpubench result", http.StatusBadRequest)
		return
	}
	if _, err := h.Store.Save(r.Context(), &f); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(summarize(f))
}

func (h *Hub) summaries(r *http.Request) ([]summary, error) {
	fs, err := h.Store.List(r.Context())
	if err != nil {
		return nil, err
	}
	out := []summary{}
	for _, f := range fs {
		out = append(out, summarize(f))
	}
	return out, nil
}

func (h *Hub) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.summaries(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func badID(id string) bool {
	return id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..")
}

func (h *Hub) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if badID(id) {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	f, err := h.Store.Load(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(f)
}

// compare renders selected results, or with latest=1 the newest result of
// every machine against the oldest machine (or ?baseline=).
func (h *Hub) compare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var files []*result.File
	if q.Get("latest") == "1" {
		all, err := h.Store.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for i := len(all) - 1; i >= 0; i-- { // oldest first so the column order is stable
			f := all[i]
			files = append(files, &f)
		}
		files = app.LatestPerLabel(files)
	} else {
		for _, id := range q["id"] {
			if badID(id) {
				http.Error(w, "bad id", http.StatusBadRequest)
				return
			}
			f, err := h.Store.Load(r.Context(), id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		http.Error(w, "no results to compare", http.StatusBadRequest)
		return
	}
	files = app.MergeByLabel(files)
	base := q.Get("baseline")
	var guessed []string
	if base == "" {
		base, guessed = app.DefaultBaseline(files), app.AmbiguousBaseline(files)
	}
	opts := port.ReportOptions{Baseline: base, BaselineGuessed: guessed, Title: "cpubench comparison"}
	if q.Get("format") == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		h.Markdown.Render(w, files, opts)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.HTML.Render(w, files, opts); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- nodes and jobs ----

func (h *Hub) poll(w http.ResponseWriter, r *http.Request) {
	var p control.Poll
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil || p.Node.Name == "" {
		http.Error(w, "bad poll", http.StatusBadRequest)
		return
	}
	deadline := time.After(h.PollWait)
	for {
		h.mu.Lock()
		h.board.Seen(p.Node, time.Now())
		h.notify(h.board.Reconcile(p.Node.Name, p.Running, time.Now())...)
		reply := control.PollReply{Cancel: h.board.Cancels(p.Node.Name, p.Running), Job: h.board.Dispatch(p.Node.Name, time.Now())}
		var ch chan struct{}
		if reply.Job == nil && len(reply.Cancel) == 0 {
			if ch = h.wake[p.Node.Name]; ch == nil {
				ch = make(chan struct{})
				h.wake[p.Node.Name] = ch
			}
		}
		h.mu.Unlock()
		if ch == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(reply)
			return
		}
		select {
		case <-ch:
		case <-deadline:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(control.PollReply{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Hub) status(w http.ResponseWriter, r *http.Request) {
	var st control.Status
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&st); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.notify(h.board.Update(r.PathValue("id"), st, time.Now())...)
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) jobs(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	out := struct {
		Nodes []control.Node `json:"nodes"`
		Jobs  []control.Job  `json:"jobs"`
	}{h.board.SortedNodes(), h.board.Recent(100)}
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func validProfile(p string) bool { _, ok := app.Profiles[p]; return ok }

func (h *Hub) runAuto(w http.ResponseWriter, r *http.Request) {
	profile, node := r.FormValue("profile"), r.FormValue("node")
	if !validProfile(profile) {
		http.Error(w, "unknown profile", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	var targets []string
	now := time.Now()
	for _, n := range h.board.SortedNodes() {
		if (node == "*" && n.Online(now)) || n.Name == node {
			targets = append(targets, n.Name)
		}
	}
	for _, n := range targets {
		h.board.AddAuto(newID("auto"), n, profile, now)
	}
	h.notify(targets...)
	h.mu.Unlock()
	if len(targets) == 0 {
		http.Error(w, "no such node online", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Hub) runWeb(w http.ResponseWriter, r *http.Request) {
	profile, sut, lg := r.FormValue("profile"), r.FormValue("sut"), r.FormValue("loadgen")
	if !validProfile(profile) || sut == "" || lg == "" || sut == lg {
		http.Error(w, "pick a profile and two different machines (server and load generator)", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok1 := h.board.Nodes[sut]
	_, ok2 := h.board.Nodes[lg]
	if !ok1 || !ok2 {
		http.Error(w, "unknown node", http.StatusBadRequest)
		return
	}
	target := fmt.Sprintf("http://%s:%d", s.Addr, s.WebPort)
	h.board.AddWebTest(newID("serve"), newID("load"), sut, lg, profile, target, time.Now())
	h.notify(sut)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Hub) cancel(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.notify(h.board.Cancel(r.FormValue("id"), time.Now())...)
	h.mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- page ----

type nodeView struct {
	control.Node
	Online bool
	Job    *control.Job
}

type indexData struct {
	Nodes    []nodeView
	Jobs     []control.Job
	Results  []summary
	Profiles []string
	Active   bool
	Secured  bool
}

func (h *Hub) index(w http.ResponseWriter, r *http.Request) {
	res, err := h.summaries(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := indexData{Results: res, Profiles: []string{"smoke", "quick", "standard", "full"}, Secured: h.Token != ""}
	h.mu.Lock()
	now := time.Now()
	for _, n := range h.board.SortedNodes() {
		v := nodeView{Node: n, Online: n.Online(now)}
		if j := h.board.RunningOn(n.Name); j != nil {
			c := *j
			v.Job = &c
		}
		d.Nodes = append(d.Nodes, v)
	}
	d.Jobs = h.board.Recent(30)
	d.Active = h.board.Active()
	h.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	indexTmpl.Execute(w, d)
}
