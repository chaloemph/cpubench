package cli

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/control"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

// NodeDeps talk to the hub's job API.
type NodeDeps struct {
	Poll   func(ctx context.Context, hubURL, token string, p control.Poll) (control.PollReply, error)
	Report func(ctx context.Context, hubURL, token, jobID string, st control.Status) error
}

func init() {
	register("node", "connect this machine to a hub so tests start from the hub's buttons", cmdNode)
}

type node struct {
	d     *Deps
	hub   string
	token string
	info  control.Node
	meter port.PowerMeter
	// listenPort is where web serve binds; info.WebPort is what others dial.
	listenPort int
	mu         sync.Mutex
	cancel     map[string]context.CancelFunc
}

func cmdNode(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "node", "--hub http://hub:8090 [--token T] [--name N] [--machine M] [--power SRC]")
	hub := fs.String("hub", "", "hub URL")
	token := fs.String("token", os.Getenv("CPUBENCH_TOKEN"), "hub token ($CPUBENCH_TOKEN)")
	name := fs.String("name", "", "node name shown in the hub and used as result label (default: hostname)")
	machine := fs.String("machine", "", "physical server this node runs on; set it for VM/container nodes so the report shows layer overhead (default: --name)")
	powerSrc := fs.String("power", "", "this machine's power source (credentials stay on this machine)")
	addr := fs.String("addr", "", "address other nodes use to reach this one for web tests (default: detected)")
	webPort := fs.Int("web-port", 8080, "port `web serve` listens on during web tests")
	advPort := fs.Int("advertise-port", 0, "port load generators connect to, if different (e.g. a Kubernetes NodePort; default: --web-port)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *hub == "" {
		fs.Usage()
		return fmt.Errorf("--hub is required")
	}
	sys, err := d.App.Probe.System(ctx)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = sys.Hostname
	}
	if *addr == "" {
		*addr = outboundIP(*hub)
		if sys.Context.Container != "" {
			fmt.Fprintf(d.Stderr, "warning: running in a container; web tests will dial %s. With bridge networking pass --addr <host-ip> (and publish the web port)\n", *addr)
		}
	}
	n := &node{d: d, hub: *hub, token: *token, cancel: map[string]context.CancelFunc{},
		info: control.Node{Name: *name, Machine: *machine, Hostname: sys.Hostname, Arch: sys.CPU.Arch, CPU: sys.CPU.Model,
			Version: d.Version, Addr: *addr, WebPort: *webPort}, listenPort: *webPort}
	if *advPort > 0 {
		n.info.WebPort = *advPort
	}
	if *powerSrc != "" {
		if n.meter, err = d.OpenPower(*powerSrc); err != nil {
			return err
		}
		if w, err := n.meter.Watts(ctx); err != nil {
			fmt.Fprintf(d.Stderr, "warning: power source not readable yet: %v\n", err)
		} else {
			fmt.Fprintf(d.Stdout, "power: %s reads %.0f W\n", n.meter.Source(), w)
		}
		n.info.HasPower = true
	}
	fmt.Fprintf(d.Stdout, "node %q (%s, %s) connected to %s · web tests reach it at %s:%d\nwaiting for jobs, Ctrl+C to stop\n",
		n.info.Name, n.info.Arch, n.info.CPU, *hub, *addr, n.info.WebPort)
	return n.loop(ctx)
}

// outboundIP is the local address used to reach the hub, a good default for
// what other machines can reach.
func outboundIP(hub string) string {
	u, err := url.Parse(hub)
	if err != nil {
		return "127.0.0.1"
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "80")
	}
	c, err := net.Dial("udp", host)
	if err != nil {
		return "127.0.0.1"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func (n *node) running() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var ids []string
	for id := range n.cancel {
		ids = append(ids, id)
	}
	return ids
}

func (n *node) loop(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		reply, err := n.d.Node.Poll(ctx, n.hub, n.token, control.Poll{Node: n.info, Running: n.running()})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			fmt.Fprintf(n.d.Stderr, "hub: %v (retrying in %s)\n", err, backoff)
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		n.mu.Lock()
		for _, id := range reply.Cancel {
			if c, ok := n.cancel[id]; ok {
				c()
			}
		}
		n.mu.Unlock()
		if j := reply.Job; j != nil {
			jctx, cancel := context.WithCancel(ctx)
			n.mu.Lock()
			n.cancel[j.ID] = cancel
			n.mu.Unlock()
			go n.run(jctx, *j)
		}
	}
	return nil
}

func (n *node) report(j control.Job, st control.Status) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := n.d.Node.Report(ctx, n.hub, n.token, j.ID, st); err != nil {
		fmt.Fprintf(n.d.Stderr, "report %s: %v\n", j.ID, err)
	}
}

func (n *node) run(ctx context.Context, j control.Job) {
	defer func() {
		n.mu.Lock()
		if c, ok := n.cancel[j.ID]; ok {
			c()
			delete(n.cancel, j.ID)
		}
		n.mu.Unlock()
	}()
	fmt.Fprintf(n.d.Stdout, "job %s: %s %s\n", j.ID, j.Kind, j.Profile)
	n.report(j, control.Status{State: control.Running, Stage: "starting"})
	switch j.Kind {
	case control.Auto:
		f, err := n.d.App.Auto(ctx, app.AutoOptions{Label: j.Label, Machine: j.Machine, Profile: j.Profile, Power: n.meter,
			Stage: func(s string) { n.report(j, control.Status{State: control.Running, Stage: s}) }})
		n.finish(ctx, j, f, err)
	case control.WebServe:
		errc := make(chan error, 1)
		go func() { errc <- n.d.Web.ServeWeb(ctx, fmt.Sprintf(":%d", n.listenPort), "", n.meter) }()
		select {
		case err := <-errc:
			n.report(j, control.Status{State: control.Failed, Message: fmt.Sprint("web serve: ", err)})
			return
		case <-time.After(time.Second):
		}
		n.report(j, control.Status{State: control.Running, Stage: control.ReadyStage})
		err := <-errc
		switch {
		case ctx.Err() != nil:
			n.report(j, control.Status{State: control.Done, Message: "server stopped after the load test"})
		default:
			n.report(j, control.Status{State: control.Failed, Message: fmt.Sprint("web serve: ", err)})
		}
	case control.WebLoad:
		w, err := webOptions(n.d, j.Target, "mix", 100, 512)
		if err != nil {
			n.report(j, control.Status{State: control.Failed, Message: err.Error()})
			return
		}
		w.Label, w.Machine = j.Label, j.Machine
		if p, ok := app.Profiles[j.Profile]; ok {
			w.Scenario.Ramp.StepDuration = p.WebStep
		}
		n.report(j, control.Status{State: control.Running, Stage: "load test"})
		f, err := n.d.App.Load(ctx, *w)
		n.finish(ctx, j, f, err)
	default:
		n.report(j, control.Status{State: control.Failed, Message: "this node does not know job type " + string(j.Kind)})
	}
}

// finish saves the result locally, uploads it to the hub and reports the outcome.
func (n *node) finish(ctx context.Context, j control.Job, f *result.File, runErr error) {
	st := control.Status{State: control.Done}
	switch {
	case ctx.Err() != nil:
		st = control.Status{State: control.Canceled, Message: "cancelled"}
	case runErr != nil:
		st = control.Status{State: control.Failed, Message: runErr.Error()}
	}
	if f != nil {
		if path, err := n.d.Store.Save(context.Background(), f); err == nil {
			fmt.Fprintf(n.d.Stdout, "job %s: saved %s\n", j.ID, path)
		}
		uctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := n.d.Upload(uctx, n.hub, n.token, f)
		cancel()
		if err != nil {
			st.Message = joinMsg(st.Message, "upload failed: "+err.Error())
		} else {
			st.ResultID = f.ID
		}
	}
	fmt.Fprintf(n.d.Stdout, "job %s: %s %s\n", j.ID, st.State, st.Message)
	n.report(j, st)
}

func joinMsg(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
