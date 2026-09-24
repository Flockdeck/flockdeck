package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ghClient is a small GitHub REST client. Two are made: one holding
// DEPLOY_DRIFT_TOKEN, which only ever reads the other repos, and one holding
// the workflow's GITHUB_TOKEN, which only ever touches the tracking issue.
// Keeping them apart means the broad-reading token is never the one that can
// write, and the writing token is never asked to read a private repo it
// cannot see.
type ghClient struct {
	base  string
	token string
	hc    *http.Client
}

func newClient(base, token string) *ghClient {
	return &ghClient{base: strings.TrimRight(base, "/"), token: token, hc: &http.Client{Timeout: 30 * time.Second}}
}

// apiError carries the status so callers can say what a 404 on a private repo
// most likely means instead of dumping a response body.
type apiError struct {
	Method, Path string
	Status       int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.Path, e.Status)
}

// do sends one request. A non-2xx status is an error; the response body is
// never included in it, because it is API-returned text and ends up in logs.
func (c *ghClient) do(ctx context.Context, method, path, accept string, body any, out any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &apiError{Method: method, Path: path, Status: resp.StatusCode}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("%s %s: decoding response: %w", method, path, err)
		}
	}
	return data, nil
}

func (c *ghClient) getJSON(ctx context.Context, path string, out any) error {
	_, err := c.do(ctx, http.MethodGet, path, "", nil, out)
	return err
}

func esc(s string) string { return url.PathEscape(s) }
