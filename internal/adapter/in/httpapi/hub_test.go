package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/adapter/out/hubclient"
	"github.com/chaloemph/cpubench/internal/adapter/out/report"
	"github.com/chaloemph/cpubench/internal/adapter/out/store/jsonfile"
	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

func res(id, label, arch string, v float64) *result.File {
	f := &result.File{Schema: result.Schema, ID: id, Label: label, Machine: label, Command: "run", CreatedAt: time.Now(),
		Benchmarks: []benchmark.Result{{Name: "int/json", Category: benchmark.Integer, Kind: benchmark.Throughput,
			Unit: "Krec/s", Threads: 1, Stats: benchmark.Stats{Median: v, N: 3}, Checksum: "1"}}}
	f.System.CPU.Arch = arch
	return f
}

func TestHubUploadListCompare(t *testing.T) {
	h := &Hub{Store: jsonfile.Store{Dir: t.TempDir()}, HTML: report.HTML{}, Markdown: report.Markdown{}, Token: "s3cret"}
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx := context.Background()

	if err := hubclient.Upload(ctx, srv.URL, "wrong", res("a1", "x86", "amd64", 100)); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401, got %v", err)
	}
	for _, f := range []*result.File{res("a1", "x86", "amd64", 100), res("b2", "arm", "arm64", 130)} {
		if err := hubclient.Upload(ctx, srv.URL, "s3cret", f); err != nil {
			t.Fatal(err)
		}
	}
	bad := res("../evil", "x", "amd64", 1)
	if err := hubclient.Upload(ctx, srv.URL, "s3cret", bad); err == nil {
		t.Fatal("path traversal id accepted")
	}

	resp, _ := http.Get(srv.URL + "/api/results")
	var list []summary
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
	resp, _ = http.Get(srv.URL + "/compare?id=a1&id=b2&baseline=x86")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "+30%") {
		t.Fatalf("compare %d: %.300s", resp.StatusCode, body)
	}
	resp, _ = http.Get(srv.URL + "/compare?id=a1&id=b2&format=md")
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "| int/json | 1 |") {
		t.Fatalf("markdown: %s", body)
	}
	resp, _ = http.Get(srv.URL + "/")
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `value="b2"`) {
		t.Fatal("index missing result")
	}
	resp, _ = http.Get(srv.URL + "/api/results/..%2Fx")
	if resp.StatusCode == 200 {
		t.Fatal("traversal on get")
	}
}

func TestFetchFromTwoHubs(t *testing.T) {
	ctx := context.Background()
	var urls []string
	for i, f := range []*result.File{res("x1", "x86-01", "amd64", 100), res("a1", "arm-01", "arm64", 140)} {
		h := &Hub{Store: jsonfile.Store{Dir: t.TempDir()}, HTML: report.HTML{}, Markdown: report.Markdown{}}
		srv := httptest.NewServer(h.Handler())
		defer srv.Close()
		if err := hubclient.Upload(ctx, srv.URL, "", f); err != nil {
			t.Fatal(i, err)
		}
		urls = append(urls, srv.URL)
	}
	var all []*result.File
	for _, u := range urls {
		got, err := hubclient.FetchAll(ctx, u+"/")
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
	}
	if len(all) != 2 || all[0].Label != "x86-01" || all[1].Benchmarks[0].Stats.Median != 140 {
		t.Fatalf("%+v", all)
	}
}
