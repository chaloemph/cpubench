// Package control is the job board behind the hub's Run buttons: which
// machines (nodes) are connected, which jobs they should run, and how a web
// test pairs a server job with a load-generator job.
package control

import (
	"slices"
	"time"
)

type Kind string

const (
	Auto     Kind = "auto"      // full single-machine test (micro, stress, power, density)
	WebServe Kind = "web-serve" // run the web target until cancelled
	WebLoad  Kind = "web-load"  // load test the paired server
)

type State string

const (
	Waiting  State = "waiting" // load job waiting for its server to be ready
	Queued   State = "queued"
	Running  State = "running"
	Done     State = "done"
	Failed   State = "failed"
	Canceled State = "canceled"
)

// OfflineAfter is how long a node may go without polling before it is shown offline.
const OfflineAfter = 60 * time.Second

type Node struct {
	Name     string    `json:"name"`
	Machine  string    `json:"machine,omitempty"` // physical server this node runs on (VM/container nodes share it)
	Hostname string    `json:"hostname"`
	Arch     string    `json:"arch"`
	CPU      string    `json:"cpu"`
	Version  string    `json:"version"`
	Addr     string    `json:"addr"` // address load generators use to reach this node
	WebPort  int       `json:"web_port"`
	HasPower bool      `json:"has_power"`
	LastSeen time.Time `json:"last_seen"`
}

func (n Node) Online(now time.Time) bool { return now.Sub(n.LastSeen) < OfflineAfter }

type Job struct {
	ID              string    `json:"id"`
	Node            string    `json:"node"`
	Kind            Kind      `json:"kind"`
	Profile         string    `json:"profile"`
	Label           string    `json:"label"`            // result label (web-load: the server's name)
	Machine         string    `json:"machine"`          // result machine group (web-load: the server's)
	Target          string    `json:"target,omitempty"` // web-load: base URL of the server
	Pair            string    `json:"pair,omitempty"`   // the other job of a web test
	State           State     `json:"state"`
	Stage           string    `json:"stage,omitempty"`
	Message         string    `json:"message,omitempty"`
	ResultID        string    `json:"result_id,omitempty"`
	CancelRequested bool      `json:"cancel_requested,omitempty"`
	Created         time.Time `json:"created"`
	Updated         time.Time `json:"updated"`
}

func (j Job) Finished() bool { return j.State == Done || j.State == Failed || j.State == Canceled }

// Poll is what a node sends; PollReply is what it gets back.
type Poll struct {
	Node    Node     `json:"node"`
	Running []string `json:"running"`
}

type PollReply struct {
	Job    *Job     `json:"job,omitempty"`
	Cancel []string `json:"cancel,omitempty"`
}

// Status is a progress report from a node about one job.
type Status struct {
	State    State  `json:"state"`
	Stage    string `json:"stage,omitempty"`
	Message  string `json:"message,omitempty"`
	ResultID string `json:"result_id,omitempty"`
}

// ReadyStage is the stage a web-serve job reports once it accepts requests.
const ReadyStage = "ready"

// Board holds nodes and jobs. It is not safe for concurrent use; the hub
// adapter guards it with a mutex.
type Board struct {
	Nodes map[string]*Node
	Jobs  []*Job
}

func NewBoard() *Board { return &Board{Nodes: map[string]*Node{}} }

func (b *Board) Job(id string) *Job {
	for _, j := range b.Jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// Seen records a poll.
func (b *Board) Seen(n Node, now time.Time) {
	n.LastSeen = now
	b.Nodes[n.Name] = &n
}

// Reconcile fails jobs the board thinks a node is running but the node no
// longer reports (it restarted, e.g. after an OOM kill). Jobs are added to
// the node's running list as soon as it receives them, so this is safe.
func (b *Board) Reconcile(node string, running []string, now time.Time) (wake []string) {
	for _, j := range b.Jobs {
		if j.Node == node && j.State == Running && !slices.Contains(running, j.ID) {
			wake = append(wake, b.Update(j.ID, Status{State: Failed,
				Message: "node lost the job (restarted or killed, e.g. out of memory)"}, now)...)
		}
	}
	return wake
}

// busy reports whether a node already has a job dispatched and unfinished.
func (b *Board) busy(node string) bool {
	return slices.ContainsFunc(b.Jobs, func(j *Job) bool { return j.Node == node && j.State == Running })
}

// Dispatch hands the oldest queued job to an idle node and marks it running.
func (b *Board) Dispatch(node string, now time.Time) *Job {
	if b.busy(node) {
		return nil
	}
	for _, j := range b.Jobs {
		if j.Node == node && j.State == Queued {
			j.State, j.Updated = Running, now
			c := *j
			return &c
		}
	}
	return nil
}

// Cancels lists jobs the node is running that should stop.
func (b *Board) Cancels(node string, running []string) []string {
	var out []string
	for _, id := range running {
		j := b.Job(id)
		if j == nil || j.Node != node || j.CancelRequested || j.Finished() {
			out = append(out, id) // unknown or finished jobs stop too
		}
	}
	return out
}

// AddAuto queues a full test on a node.
// machineOf is the physical machine a node runs on (defaults to its name).
func (b *Board) machineOf(node string) string {
	if n, ok := b.Nodes[node]; ok && n.Machine != "" {
		return n.Machine
	}
	return node
}

func (b *Board) AddAuto(id, node, profile string, now time.Time) *Job {
	j := &Job{ID: id, Node: node, Kind: Auto, Profile: profile, Label: node, Machine: b.machineOf(node), State: Queued, Created: now, Updated: now}
	b.Jobs = append(b.Jobs, j)
	return j
}

// AddWebTest queues a server job on sut and a load job on loadgen that starts
// once the server reports ready. Results are labelled with the server's name
// so they merge with its single-machine results.
func (b *Board) AddWebTest(serveID, loadID, sut, loadgen, profile, target string, now time.Time) (*Job, *Job) {
	m := b.machineOf(sut)
	s := &Job{ID: serveID, Node: sut, Kind: WebServe, Profile: profile, Label: sut, Machine: m, Pair: loadID, State: Queued, Created: now, Updated: now}
	l := &Job{ID: loadID, Node: loadgen, Kind: WebLoad, Profile: profile, Label: sut, Machine: m, Target: target, Pair: serveID, State: Waiting, Created: now, Updated: now}
	b.Jobs = append(b.Jobs, s, l)
	return s, l
}

// Update applies a node's status report and advances paired web jobs. It
// returns the nodes that should be woken because they have new work.
func (b *Board) Update(id string, st Status, now time.Time) (wake []string) {
	j := b.Job(id)
	if j == nil || j.Finished() {
		return nil
	}
	j.State, j.Stage, j.Message, j.Updated = st.State, st.Stage, st.Message, now
	if st.ResultID != "" {
		j.ResultID = st.ResultID
	}
	pair := b.Job(j.Pair)
	if pair == nil {
		return nil
	}
	switch {
	case j.Kind == WebServe && st.State == Running && st.Stage == ReadyStage && pair.State == Waiting:
		pair.State, pair.Updated = Queued, now
		return []string{pair.Node}
	case j.Kind == WebServe && j.Finished() && !pair.Finished():
		pair.State, pair.Message, pair.Updated = Canceled, "server job ended: "+string(j.State), now
		return []string{pair.Node}
	case j.Kind == WebLoad && j.Finished() && !pair.Finished():
		pair.CancelRequested, pair.Updated = true, now
		return []string{pair.Node}
	}
	return nil
}

// Cancel stops a job: queued/waiting jobs end now, running ones are asked to stop.
func (b *Board) Cancel(id string, now time.Time) (wake []string) {
	j := b.Job(id)
	if j == nil || j.Finished() {
		return nil
	}
	if j.State == Running {
		j.CancelRequested, j.Updated = true, now
		wake = append(wake, j.Node)
	} else {
		j.State, j.Message, j.Updated = Canceled, "cancelled before start", now
	}
	if p := b.Job(j.Pair); p != nil && !p.Finished() {
		wake = append(wake, b.Cancel(p.ID, now)...)
	}
	return wake
}

// Active reports whether any job is still in progress.
func (b *Board) Active() bool {
	return slices.ContainsFunc(b.Jobs, func(j *Job) bool { return !j.Finished() })
}

// Recent returns jobs newest first, at most n.
func (b *Board) Recent(n int) []Job {
	var out []Job
	for i := len(b.Jobs) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, *b.Jobs[i])
	}
	return out
}

// SortedNodes returns nodes by name.
func (b *Board) SortedNodes() []Node {
	var out []Node
	for _, n := range b.Nodes {
		out = append(out, *n)
	}
	slices.SortFunc(out, func(a, c Node) int {
		switch {
		case a.Name < c.Name:
			return -1
		case a.Name > c.Name:
			return 1
		}
		return 0
	})
	return out
}

// Running returns the unfinished job dispatched to a node, if any.
func (b *Board) RunningOn(node string) *Job {
	for _, j := range b.Jobs {
		if j.Node == node && j.State == Running {
			return j
		}
	}
	return nil
}
