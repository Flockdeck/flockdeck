package helpers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultAllowURL is the rule every request and every redirect is held to:
// https, to github.com or a subdomain of githubusercontent.com (where GitHub
// serves release assets from). Anything else, including plain http, another
// port on github.com, a name that merely ends in those words and a URL with
// credentials in it, is refused.
func DefaultAllowURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	if u.Port() != "" && u.Port() != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

const (
	maxRedirects = 5
	// requestLimit is the longest one request, body included, may take. The
	// stall timeout is what ends a download that has stopped; this ends one that
	// trickles.
	requestLimit = time.Hour
)

// errNotFound is a 404, which for the signature file means "not signed" and
// for anything else means there is no such release.
var errNotFound = errors.New("not found")

// newClient builds the HTTP client: no credentials, no cookies, and a redirect
// check that applies the allowlist to every hop.
func newClient(allow func(*url.URL) bool) *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if !allow(req.URL) {
				return fmt.Errorf("refused a redirect to %s: helpers are only downloaded from github.com", describeURL(req.URL))
			}
			return nil
		},
	}
}

// describeURL is a URL for a message: scheme and host, no path or query.
func describeURL(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

// stallError is what a request that went a stall timeout with nothing arriving
// fails with.
func stallError(host string, d time.Duration) error {
	return fmt.Errorf("%s stopped sending for %s, so the request was given up on; try again", host, d)
}

// response is an open response whose body restarts a stall timer with every
// read.
type response struct {
	body    io.ReadCloser
	timer   *time.Timer
	stalled *atomic.Bool
	cancel  context.CancelFunc
	stall   time.Duration
	host    string
	length  int64
}

func (r *response) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.timer.Reset(r.stall)
	}
	if err != nil && err != io.EOF && r.stalled.Load() {
		return n, stallError(r.host, r.stall)
	}
	return n, err
}

func (r *response) Close() error {
	err := r.body.Close()
	r.timer.Stop()
	r.cancel()
	return err
}

// get opens a URL. The caller closes the response. The allowlist is applied to
// the first URL here and to every redirect by the client.
func (in *Installer) get(ctx context.Context, rawURL string) (*response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !in.allow(u) {
		return nil, fmt.Errorf("refused to fetch %s: helpers are only downloaded from github.com", describeURL(u))
	}
	ctx, cancel := context.WithTimeout(ctx, requestLimit)
	var stalled atomic.Bool
	timer := time.AfterFunc(in.stall, func() { stalled.Store(true); cancel() })
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	req.Header.Set("User-Agent", "flockdeck-helpers")
	resp, err := in.client.Do(req)
	if err != nil {
		timer.Stop()
		cancel()
		if stalled.Load() {
			return nil, stallError(u.Host, in.stall)
		}
		return nil, err
	}
	r := &response{body: resp.Body, timer: timer, stalled: &stalled, cancel: cancel, stall: in.stall, host: u.Host, length: resp.ContentLength}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		r.Close()
		return nil, fmt.Errorf("%s: %w", describeURL(u)+u.Path, errNotFound)
	case resp.StatusCode != http.StatusOK:
		r.Close()
		return nil, fmt.Errorf("%s: %s", describeURL(u)+u.Path, resp.Status)
	}
	return r, nil
}

// fetchSmall reads a whole small file, and fails if it is larger than limit,
// without reading past limit+1 bytes.
func (in *Installer) fetchSmall(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	r, err := in.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if r.length > limit {
		return nil, fmt.Errorf("%s is larger than the %d bytes allowed", rawURL, limit)
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is larger than the %d bytes allowed", rawURL, limit)
	}
	return body, nil
}

// download writes a URL to dest, reading at most limit bytes, and checks it
// against the SHA-256 the signed checksums give. A file that is too large, or
// does not match, is deleted. The hash is of exactly the bytes written.
func (in *Installer) download(ctx context.Context, rawURL, dest string, limit int64, want string) error {
	r, err := in.get(ctx, rawURL)
	if err != nil {
		return err
	}
	defer r.Close()
	if r.length > limit {
		return fmt.Errorf("the download is %d bytes, over the %d byte limit", r.length, limit)
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest)
		return err
	}
	if n > limit {
		os.Remove(dest)
		return fmt.Errorf("the download is larger than the %d byte limit", limit)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(dest)
		return &ChecksumError{Got: got, Want: want}
	}
	return nil
}

// ChecksumError is a download that does not match the signed checksums.
type ChecksumError struct{ Got, Want string }

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("the download does not match the checksum in checksums.txt (got %s, want %s)", e.Got[:12], e.Want[:12])
}
