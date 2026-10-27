package apikey

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// ErrKeyRefused is an endpoint refusing a key it was asked to check.
var ErrKeyRefused = errors.New("the API refused the key")

// ErrUnreachable is an endpoint that could not be reached to be asked at all.
// It says nothing about the key.
var ErrUnreachable = errors.New("the endpoint could not be reached")

// bodyMax bounds what is read of any answer; only the status matters.
const bodyMax = 8 << 10

// CheckKey asks an endpoint whether it takes a key, by listing its models --
// the one request every wire has that costs nothing and needs the key. It
// reports nil where the key was taken, an error wrapping ErrKeyRefused where it
// was refused, ErrUnreachable where nothing answered, and another error where
// the endpoint could not be asked or answered something else.
//
// No error carries the endpoint's own words: some vendors quote part of the key
// back in a refusal, and an error is the string that gets pasted into bug
// reports. Only the status is reported.
func CheckKey(ctx context.Context, wire, baseURL, key string) error {
	var (
		u      string
		header = http.Header{}
		w      = strings.ToLower(strings.TrimSpace(wire))
	)
	switch w {
	case "anthropic", "":
		u = endpoint(baseURL, "https://api.anthropic.com", "v1", "/models?limit=1")
		header.Set("anthropic-version", "2023-06-01")
		setIf(header, "x-api-key", key)
	case "openai", "openai-compatible":
		u = openaiEndpoint(baseURL, "/models")
		if key != "" {
			header.Set("Authorization", "Bearer "+key)
		}
	case "gemini", "google":
		u = endpoint(baseURL, "https://generativelanguage.googleapis.com", "v1beta", "/models?pageSize=1")
		setIf(header, "x-goog-api-key", key)
	default:
		return fmt.Errorf("unknown wire %q: it must be anthropic, openai or gemini", wire)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header = header
	resp, err := client.Do(req)
	if err != nil {
		if unreachable(err) {
			return ErrUnreachable
		}
		return fmt.Errorf("could not ask %s", req.URL.Host)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w (%s)", ErrKeyRefused, resp.Status)
	case resp.StatusCode == http.StatusBadRequest && (w == "gemini" || w == "google"):
		// Gemini refuses a bad key with a 400 whose message says so.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, bodyMax))
		if strings.Contains(strings.ToLower(string(msg)), "api key not valid") {
			return fmt.Errorf("%w (%s)", ErrKeyRefused, resp.Status)
		}
	}
	return fmt.Errorf("the endpoint answered %s", resp.Status)
}

func setIf(h http.Header, name, v string) {
	if v != "" {
		h.Set(name, v)
	}
}

// client follows a redirect only on the host that was asked, and never from
// https down to plain http: the key travels in a header Go does not drop.
var client = &http.Client{CheckRedirect: keepToTheAddress}

func keepToTheAddress(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	asked := via[0].URL
	switch {
	case !strings.EqualFold(req.URL.Host, asked.Host):
		return fmt.Errorf("the endpoint redirected to %s, which is not followed: the key would be sent there", req.URL.Host)
	case asked.Scheme == "https" && req.URL.Scheme != "https":
		return errors.New("the endpoint redirected from https to plain http, which is not followed: the key would be sent unencrypted")
	}
	return nil
}

func unreachable(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}

var apiVersionRE = regexp.MustCompile(`^v\d+((alpha|beta)\d*)?$`)

// endpoint builds a request URL for the Anthropic and Gemini wires from a base
// that may or may not already name the API version.
func endpoint(base, fallback, version, path string) string {
	return joinEndpoint(base, fallback, version, path, func(p string) bool {
		return apiVersionRE.MatchString(p[strings.LastIndex(p, "/")+1:])
	})
}

// openaiEndpoint builds a request URL for the OpenAI wire. The version is added
// only to a bare root; a base with a path of its own is taken as the API root.
func openaiEndpoint(base, path string) string {
	return joinEndpoint(base, "https://api.openai.com", "v1", path, func(p string) bool { return p != "" })
}

// joinEndpoint puts the API's path after a base's own, with the version between
// them where versioned says the base's path lacks it. The base is parsed so a
// query on it (Azure's ?api-version=) stays a query.
func joinEndpoint(base, fallback, version, path string, versioned func(basePath string) bool) string {
	b := strings.TrimSpace(base)
	if b == "" {
		b = fallback
	}
	u, err := url.Parse(b)
	if err != nil || u.Host == "" {
		return strings.TrimRight(b, "/") + "/" + version + path
	}
	p := strings.TrimRight(u.EscapedPath(), "/")
	// A pasted full request URL has the API's own path taken back off first.
	for _, tail := range []string{"/chat/completions", "/messages", "/models"} {
		p = strings.TrimRight(strings.TrimSuffix(p, tail), "/")
	}
	if !versioned(p) {
		p += "/" + version
	}
	rest, query, _ := strings.Cut(path, "?")
	p += rest
	if plain, err := url.PathUnescape(p); err == nil {
		u.Path, u.RawPath = plain, p
	}
	switch {
	case u.RawQuery == "":
		u.RawQuery = query
	case query != "":
		u.RawQuery += "&" + query
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}
