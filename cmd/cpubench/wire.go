package main

import (
	"context"

	"github.com/chaloemph/cpubench/internal/adapter/in/cli"
	"github.com/chaloemph/cpubench/internal/adapter/in/httpapi"
	"github.com/chaloemph/cpubench/internal/adapter/in/websut"
	"github.com/chaloemph/cpubench/internal/adapter/out/httpload"
	"github.com/chaloemph/cpubench/internal/adapter/out/hubclient"
	"github.com/chaloemph/cpubench/internal/adapter/out/process"
	"github.com/chaloemph/cpubench/internal/adapter/out/report"
	"github.com/chaloemph/cpubench/internal/adapter/out/scanner"
	"github.com/chaloemph/cpubench/internal/adapter/out/store/jsonfile"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/port"
)

// wire attaches the network-facing adapters.
func wire(d *cli.Deps) {
	d.Web = cli.WebDeps{
		NewTarget: func(c int, ka bool) port.LoadTarget { return httpload.NewTarget(c, ka) },
		NewSUT:    func(t string) port.SUTStats { return httpload.NewSUT(t) },
		NewAgent:  func(a string) port.LoadAgent { return httpload.NewAgent(a) },
		ServeWeb: func(ctx context.Context, addr, tlsKey string, pm port.PowerMeter) error {
			return (&websut.Server{Probe: d.App.Probe, TLSKey: tlsKey, Power: pm}).Serve(ctx, addr)
		},
		ServeAgent: func(ctx context.Context, addr string) error {
			h := httpapi.AgentHandler(func(sc load.Scenario) port.LoadTarget {
				return httpload.NewTarget(sc.Concurrency, sc.KeepAlive == nil || *sc.KeepAlive)
			}, d.App.Log)
			return httpapi.ListenAndServe(ctx, addr, h)
		},
		ServeHub: func(ctx context.Context, addr, dir, token string) error {
			h := &httpapi.Hub{Store: jsonfile.Store{Dir: dir}, HTML: report.HTML{}, Markdown: report.Markdown{}, Token: token}
			return httpapi.ListenAndServe(ctx, addr, h.Handler())
		},
	}
	d.Upload = hubclient.Upload
	d.FetchHub = hubclient.FetchAll
	d.Node = cli.NodeDeps{Poll: hubclient.Poll, Report: hubclient.Report}
	d.Ready = cli.ReadinessDeps{Process: process.Runner{}, Images: scanner.Docker{}, Files: scanner.Files{}}
}
