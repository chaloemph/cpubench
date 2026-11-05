package app

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/workload"
)

// NUMAMatrix measures pointer-chase latency from every node's CPUs to memory
// first-touched by every node. Placement relies on Linux's default
// first-touch policy: the chain is built by a thread pinned to the memory node.
func (a *App) NUMAMatrix(ctx context.Context, sys hardware.System, bufferMB int, pin bool) (*result.NUMAMatrix, error) {
	var nodes []int
	var nodeCPU []int
	for _, n := range sys.Topology.NUMANodes {
		if c := hardware.CPUsOnNode(sys.Topology, n.ID); len(c) > 0 {
			nodes = append(nodes, n.ID)
			nodeCPU = append(nodeCPU, c[0])
		}
	}
	m := &result.NUMAMatrix{Nodes: nodes, BufferMB: bufferMB, LatencyNs: make([][]float64, len(nodes))}
	const steps = 2 << 20
	for j := range nodes {
		var chain workload.Chain
		if err := a.onCPU(nodeCPU[j], pin, func() { chain = workload.NewChain(bufferMB*1024, uint64(j)) }); err != nil {
			return nil, err
		}
		for i := range nodes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var ns float64
			err := a.onCPU(nodeCPU[i], pin, func() {
				chain.Chase(steps / 4)
				t0 := time.Now()
				chain.Chase(steps)
				ns = float64(time.Since(t0).Nanoseconds()) / steps
			})
			if err != nil {
				return nil, err
			}
			if m.LatencyNs[i] == nil {
				m.LatencyNs[i] = make([]float64, len(nodes))
			}
			m.LatencyNs[i][j] = ns
			a.logf("numa  cpu node %d -> mem node %d  %7.1f ns", nodes[i], nodes[j], ns)
		}
	}
	return m, nil
}

// onCPU runs fn on a fresh OS thread pinned to cpu and waits for it.
func (a *App) onCPU(cpu int, pin bool, fn func()) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the pinned thread is discarded on exit
		if pin && a.Pinner != nil {
			if err := a.Pinner.PinCurrentThread(cpu); err != nil {
				errc <- fmt.Errorf("pin cpu %d: %w", cpu, err)
				return
			}
		}
		fn()
		errc <- nil
	}()
	return <-errc
}
