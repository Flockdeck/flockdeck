package server

import (
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AllowedHostsEnv names the environment variable that adds to the names the
// server answers to.
//
// The server listens on 127.0.0.1 and answers requests whose Host header is
// 127.0.0.1 or localhost, with any port. A browser page that reached the same
// port through some other name (a name that resolves to 127.0.0.1 only after the
// page has loaded is the usual way) sends that other name in Host, and is
// refused here before any handler, and before a WebSocket upgrade, sees it.
// The WebSocket library's own Origin check does not cover this: it accepts an
// Origin whose host equals the request's Host, and such a page's two are the
// same.
//
// The variable is a comma-separated list. An entry is a bare host, which
// matches that host on any port (or none), or host:port, which matches that
// port only. IPv6 addresses are written in brackets, as in a URL. Matching
// ignores case and a trailing dot. There are no wildcards.
//
// Set it where the UI is reached by another name: through a Kubernetes
// Service or an Ingress, or from another machine by a hostname.
const AllowedHostsEnv = "FLOCKDECK_ALLOWED_HOSTS"

// defaultAllowedHosts are the hosts the server answers to without being told:
// the address the window and the printed links use, and the name people type
// for it. [::1] is not among them, because the listener is IPv4 only.
var defaultAllowedHosts = []string{"127.0.0.1", "localhost"}

// hostAllow is the set of Host header values the main server answers to.
type hostAllow struct {
	// anyPort holds hosts that match on every port, and with no port.
	anyPort map[string]bool
	// exact holds host:port pairs, written the way splitHost returns them.
	exact map[string]bool
}

// newHostAllow returns the default set plus the entries in list, which is the
// text of AllowedHostsEnv. Entries may be separated by commas; blank ones are
// skipped. An entry that is not a plain host or host:port is an error, and the
// error names it.
func newHostAllow(list string) (*hostAllow, error) {
	a := &hostAllow{
		anyPort: map[string]bool{},
		exact:   map[string]bool{},
	}
	for _, h := range defaultAllowedHosts {
		a.anyPort[h] = true
	}
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		host, port, ok := splitHost(entry)
		if !ok {
			return nil, fmt.Errorf("%s: %q is not a host name, an IP address or host:port (wildcards, schemes and paths are not accepted; write IPv6 addresses in brackets)", AllowedHostsEnv, entry)
		}
		if port == "" {
			a.anyPort[host] = true
		} else {
			a.exact[hostKey(host, port)] = true
		}
	}
	return a, nil
}

// allows reports whether a request whose Host header is value may be served.
func (a *hostAllow) allows(value string) bool {
	host, port, ok := splitHost(value)
	if !ok {
		return false
	}
	if a.anyPort[host] {
		return true
	}
	return port != "" && a.exact[hostKey(host, port)]
}

// hostKey is how a host and port are written as a map key. An IPv6 address
// keeps its brackets so that the colon before the port is not ambiguous.
func hostKey(host, port string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// splitHost reads a Host header value, or a list entry, as a host and an
// optional port. The host comes back lower case, without a trailing dot and
// without the brackets round an IPv6 address, which is written in its
// canonical form. ok is false for anything that is not a host name, an IP
// address, or either followed by a colon and a port from 1 to 65535: an empty
// value, one with a user name, a path, a space or a control character, an
// IPv6 address without brackets, a zone, a second colon, an empty port.
func splitHost(value string) (host, port string, ok bool) {
	if value == "" || len(value) > 300 {
		return "", "", false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c <= ' ' || c >= 0x7f {
			return "", "", false
		}
	}
	rest := value
	if strings.HasPrefix(value, "[") {
		end := strings.IndexByte(value, ']')
		if end < 0 {
			return "", "", false
		}
		addr, err := netip.ParseAddr(value[1:end])
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", "", false
		}
		host = addr.String()
		rest = value[end+1:]
		switch {
		case rest == "":
			return host, "", true
		case rest[0] != ':':
			return "", "", false
		}
		port = rest[1:]
	} else {
		name := value
		if i := strings.IndexByte(value, ':'); i >= 0 {
			name, port = value[:i], value[i+1:]
			if strings.Contains(port, ":") {
				return "", "", false
			}
		}
		name = strings.ToLower(name)
		name = strings.TrimSuffix(name, ".")
		if !validHostName(name) {
			return "", "", false
		}
		host = name
		if addr, err := netip.ParseAddr(name); err == nil {
			host = addr.String()
		}
		if port == "" && !strings.HasSuffix(value, ":") {
			return host, "", true
		}
	}
	if !validPort(port) {
		return "", "", false
	}
	return host, port, true
}

// validHostName reports whether s, already lower case and without a trailing
// dot, is made of the characters a DNS name or a dotted IPv4 address can be,
// with no empty label. It does not look the name up, and does not insist that
// a label starts with a letter or digit.
func validHostName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	labelLen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.':
			if labelLen == 0 {
				return false
			}
			labelLen = 0
			continue
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
		labelLen++
		if labelLen > 63 {
			return false
		}
	}
	return labelLen > 0
}

// validPort reports whether s is a decimal port from 1 to 65535, with no sign
// and no spaces.
func validPort(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}

// hostRefusals limits how often a refused Host is written to stderr. The value
// is chosen by whoever sent the request, so it is cleaned before it is shown,
// and only the first refusal in each interval is written, with a count of the
// ones skipped.
type hostRefusals struct {
	mu      sync.Mutex
	last    time.Time
	skipped int
}

// hostRefusalLogEvery is the shortest time between two lines about refused
// hosts.
const hostRefusalLogEvery = time.Minute

// note records one refusal and returns the line to print for it, if this is
// the first in its interval.
func (h *hostRefusals) note(now time.Time, host string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.last.IsZero() && now.Sub(h.last) < hostRefusalLogEvery {
		h.skipped++
		return "", false
	}
	line := fmt.Sprintf("flockdeck: refused a request with the Host header %q, which is not in the list this server answers to; to allow it, set %s", cleanHost(host), AllowedHostsEnv)
	if h.skipped > 0 {
		line += fmt.Sprintf(" (%d more refused since the last note)", h.skipped)
	}
	h.last, h.skipped = now, 0
	return line, true
}

// cleanHost makes a Host header value fit to show: printable ASCII only, and
// no more than 60 characters.
func cleanHost(s string) string {
	const max = 60
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); i++ {
		if n == max {
			b.WriteString("...")
			break
		}
		c := s[i]
		if c < ' ' || c >= 0x7f {
			c = '?'
		}
		b.WriteByte(c)
		n++
	}
	return b.String()
}

// guardHosts wraps next so that a request whose Host header is not in the
// allowed set is answered with 403 and goes no further. It is put in front of
// the loopback listener only. Requests that arrive through the relay's tunnel
// are served by RemoteHandler, which does not pass through it: their Host is
// whatever the relay forwards, and they are let in by the relay's own check of
// the device instead.
func (s *Server) guardHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.hosts.allows(r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		if line, ok := s.hostLog.note(time.Now(), r.Host); ok {
			fmt.Fprintln(os.Stderr, line)
		}
		h := w.Header()
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Flockdeck did not answer: the Host header %q is not one this server answers to.\n"+
			"It answers to 127.0.0.1 on any port. To reach it by another name, add that name to %s\n"+
			"(comma-separated; a bare name allows any port, name:port allows one) and restart Flockdeck.\n",
			cleanHost(r.Host), AllowedHostsEnv)
	})
}
