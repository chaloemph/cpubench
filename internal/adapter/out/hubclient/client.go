// Package hubclient uploads result files to a cpubench hub.
package hubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/control"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

func Upload(ctx context.Context, hubURL, token string, f *result.File) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(hubURL, "/")+"/api/results", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// Poll asks the hub for work (long poll).
func Poll(ctx context.Context, hubURL, token string, p control.Poll) (control.PollReply, error) {
	var reply control.PollReply
	err := postJSON(ctx, strings.TrimRight(hubURL, "/")+"/api/nodes/poll", token, p, &reply, http.StatusOK)
	return reply, err
}

// Report sends a job status update.
func Report(ctx context.Context, hubURL, token, jobID string, st control.Status) error {
	return postJSON(ctx, strings.TrimRight(hubURL, "/")+"/api/jobs/"+jobID+"/status", token, st, nil, http.StatusNoContent)
}

func postJSON(ctx context.Context, url, token string, in, out any, want int) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// FetchAll downloads every result stored on a hub (read endpoints need no token).
func FetchAll(ctx context.Context, hubURL string) ([]*result.File, error) {
	base := strings.TrimRight(hubURL, "/")
	var list []struct {
		ID string `json:"id"`
	}
	if err := getJSON(ctx, base+"/api/results", &list); err != nil {
		return nil, err
	}
	var out []*result.File
	for _, s := range list {
		var f result.File
		if err := getJSON(ctx, base+"/api/results/"+s.ID, &f); err != nil {
			return nil, fmt.Errorf("%s: %w", s.ID, err)
		}
		out = append(out, &f)
	}
	return out, nil
}

func getJSON(ctx context.Context, url string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
