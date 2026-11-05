package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/adapter/out/hubclient"
	"github.com/chaloemph/cpubench/internal/adapter/out/report"
	"github.com/chaloemph/cpubench/internal/adapter/out/store/jsonfile"
	"github.com/chaloemph/cpubench/internal/domain/control"
)

func postForm(t *testing.T, srv *httptest.Server, path, pass string, v url.Values, origin string) int {
	req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if pass != "" {
		req.SetBasicAuth("admin", pass)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestButtonsDispatchToNodes(t *testing.T) {
	h := &Hub{Store: jsonfile.Store{Dir: t.TempDir()}, HTML: report.HTML{}, Markdown: report.Markdown{}, Token: "tok", PollWait: 2 * time.Second}
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx := context.Background()
	ts := control.Node{Name: "arm-01", Arch: "arm64", Addr: "10.0.0.5", WebPort: 8080}
	lg := control.Node{Name: "lg1", Arch: "amd64", Addr: "10.0.0.9", WebPort: 8080}

	if _, err := hubclient.Poll(ctx, srv.URL, "bad", control.Poll{Node: ts}); err == nil {
		t.Fatal("poll without token accepted")
	}
	// register both nodes (empty polls time out after PollWait)
	for _, n := range []control.Node{ts, lg} {
		if r, err := hubclient.Poll(ctx, srv.URL, "tok", control.Poll{Node: n}); err != nil || r.Job != nil {
			t.Fatalf("%v %+v", err, r)
		}
	}

	if code := postForm(t, srv, "/ui/run", "", url.Values{"node": {"arm-01"}, "profile": {"smoke"}}, ""); code != 401 {
		t.Fatalf("button without password: %d", code)
	}
	if code := postForm(t, srv, "/ui/run", "tok", url.Values{"node": {"arm-01"}, "profile": {"smoke"}}, "http://evil.example"); code != 403 {
		t.Fatalf("cross-site button: %d", code)
	}
	if code := postForm(t, srv, "/ui/run", "tok", url.Values{"node": {"arm-01"}, "profile": {"smoke"}}, srv.URL); code != 303 {
		t.Fatalf("run button: %d", code)
	}
	r, err := hubclient.Poll(ctx, srv.URL, "tok", control.Poll{Node: ts})
	if err != nil || r.Job == nil || r.Job.Kind != control.Auto || r.Job.Profile != "smoke" {
		t.Fatalf("%v %+v", err, r.Job)
	}
	autoID := r.Job.ID
	hubclient.Report(ctx, srv.URL, "tok", autoID, control.Status{State: control.Done, ResultID: "res1"})

	// web test: long poll on the load generator must wake when the server is ready
	if code := postForm(t, srv, "/ui/web", "tok", url.Values{"sut": {"arm-01"}, "loadgen": {"lg1"}, "profile": {"smoke"}}, ""); code != 303 {
		t.Fatalf("web button: %d", code)
	}
	serve, _ := hubclient.Poll(ctx, srv.URL, "tok", control.Poll{Node: ts})
	if serve.Job == nil || serve.Job.Kind != control.WebServe {
		t.Fatalf("%+v", serve.Job)
	}
	got := make(chan control.PollReply, 1)
	go func() {
		for { // like the real node: retry transport errors (e.g. a stale keep-alive connection)
			r, err := hubclient.Poll(ctx, srv.URL, "tok", control.Poll{Node: lg})
			if err == nil {
				got <- r
				return
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	hubclient.Report(ctx, srv.URL, "tok", serve.Job.ID, control.Status{State: control.Running, Stage: control.ReadyStage})
	var load control.PollReply
	select {
	case load = <-got:
	case <-time.After(time.Second):
		t.Fatal("load generator was not woken")
	}
	if load.Job == nil || load.Job.Target != "http://10.0.0.5:8080" || load.Job.Label != "arm-01" {
		t.Fatalf("%+v", load.Job)
	}
	hubclient.Report(ctx, srv.URL, "tok", load.Job.ID, control.Status{State: control.Done})
	stop, _ := hubclient.Poll(ctx, srv.URL, "tok", control.Poll{Node: ts, Running: []string{serve.Job.ID}})
	if len(stop.Cancel) != 1 || stop.Cancel[0] != serve.Job.ID {
		t.Fatalf("server not told to stop: %+v", stop)
	}

	resp, _ := http.Get(srv.URL + "/")
	b := new(strings.Builder)
	buf := make([]byte, 1<<16)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	for _, want := range []string{"arm-01", "lg1", "Run web test", "Compare latest of every machine", autoID} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("page missing %q", want)
		}
	}
}
