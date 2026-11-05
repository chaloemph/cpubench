// Package httpapi holds the HTTP driving adapters: the load agent and the hub.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/port"
)

// AgentHandler accepts load jobs from a coordinator (`web load --agents`).
func AgentHandler(newTarget func(sc load.Scenario) port.LoadTarget, log port.Logger) http.Handler {
	mux := http.NewServeMux()
	busy := make(chan struct{}, 1)
	mux.HandleFunc("POST /job", func(w http.ResponseWriter, r *http.Request) {
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			http.Error(w, "agent busy with another job", http.StatusConflict)
			return
		}
		var job port.LoadJob
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("job: %d phase(s), share %.2f, start %s", len(job.Phases), job.Share, job.StartAt.Format(time.TimeOnly))
		out, err := app.RunLoadJob(r.Context(), newTarget(job.Scenario), job)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return mux
}

// ListenAndServe runs h on addr until ctx ends.
func ListenAndServe(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
