// Package httpload holds the HTTP clients a load test drives: the target
// under test, a `cpubench web serve` stats endpoint, and remote load agents.
package httpload

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/port"
)

// Target sends requests with HTTP/1.1 keep-alive (or a new connection per
// request when keepAlive is false, which measures TLS/TCP handshakes).
type Target struct{ client *http.Client }

func NewTarget(concurrency int, keepAlive bool) *Target {
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        concurrency,
		MaxIdleConnsPerHost: concurrency,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   !keepAlive,
		DisableCompression:  true,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // test targets use self-signed certs
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &Target{client: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
}

func (t *Target) Do(ctx context.Context, r load.Request) (int, error) {
	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return 0, err
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, err
}

// SUT reads /_cpubench/stats and /_cpubench/info from a cpubench target.
type SUT struct {
	base   string
	client *http.Client
}

func NewSUT(target string) *SUT {
	return &SUT{base: strings.TrimRight(target, "/"), client: &http.Client{Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
}

type Stats struct {
	CPUSeconds float64 `json:"cpu_seconds"`
	NumCPU     int     `json:"num_cpu"`
	Watts      float64 `json:"watts,omitempty"`
	WattsErr   string  `json:"watts_error,omitempty"`
}

func (s *SUT) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (s *SUT) CPUSeconds(ctx context.Context) (float64, int, error) {
	var st Stats
	err := s.getJSON(ctx, "/_cpubench/stats", &st)
	return st.CPUSeconds, st.NumCPU, err
}

func (s *SUT) Watts(ctx context.Context) (float64, error) {
	var st Stats
	if err := s.getJSON(ctx, "/_cpubench/stats?power=1", &st); err != nil {
		return 0, err
	}
	if st.Watts <= 0 {
		if st.WattsErr == "" {
			st.WattsErr = "server has no power source"
		}
		return 0, fmt.Errorf("%s", st.WattsErr)
	}
	return st.Watts, nil
}

func (s *SUT) System(ctx context.Context) (hardware.System, error) {
	var sys hardware.System
	err := s.getJSON(ctx, "/_cpubench/info", &sys)
	return sys, err
}

// Agent drives a remote `cpubench web agent`.
type Agent struct {
	addr   string
	client *http.Client
}

func NewAgent(addr string) *Agent {
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return &Agent{addr: strings.TrimRight(addr, "/"), client: &http.Client{}}
}

func (a *Agent) Name() string { return a.addr }

func (a *Agent) Run(ctx context.Context, job port.LoadJob) (*port.LoadOutcome, error) {
	var total time.Duration
	for _, p := range job.Phases {
		total += p.Duration
	}
	ctx, cancel := context.WithTimeout(ctx, time.Until(job.StartAt)+total+time.Minute)
	defer cancel()
	b, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.addr+"/job", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var out port.LoadOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
