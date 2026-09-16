package remote

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// serveNothing answers whatever comes down a tunnel with a 404.
func serveNothing(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }

// A relay that refuses this machine because its account's trial or
// subscription has run out is not a revocation: the window is told in the
// relay's own words, and the relay is asked again now and then rather than
// given up on, so that paying for the account brings remote access back.
func TestALapsedAccountIsToldAndTriedAgainSlowly(t *testing.T) {
	quick(t)
	old := lapsedRetry
	lapsedRetry = 300 * time.Millisecond
	t.Cleanup(func() { lapsedRetry = old })
	f := newFakeRelay(t)
	f.mu.Lock()
	f.refuse = http.StatusPaymentRequired
	f.mu.Unlock()
	c := NewConnector(f.config(), "", serveNothing, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "lapsed", func() bool { return c.Status().State == StateLapsed })
	if d := c.Status().Detail; d != "this host was removed" {
		t.Errorf("the status says %q, want the relay's own words as they are", d)
	}
	if c.Status().RetryAt.IsZero() {
		t.Error("the status does not say when the relay is asked again")
	}
	time.Sleep(100 * time.Millisecond) // many backoffs' worth, well short of lapsedRetry
	if n := f.count(); n != 1 {
		t.Errorf("the relay was asked %d times, want once until the slow retry", n)
	}
	f.mu.Lock()
	f.refuse = 0
	f.mu.Unlock()
	waitFor(t, "connected once the account is paid for", func() bool { return c.Status().State == StateConnected })
}

// A tunnel closed because the account ran out while it was open is the same,
// with the reason the relay closed it with; and Try again asks at once.
func TestATunnelClosedForItsPlanIsLapsed(t *testing.T) {
	quick(t)
	old := lapsedRetry
	lapsedRetry = time.Minute
	t.Cleanup(func() { lapsedRetry = old })
	f := newFakeRelay(t)
	f.mu.Lock()
	f.closeWith = CloseLapsed
	f.mu.Unlock()
	c := NewConnector(f.config(), "", serveNothing, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "lapsed", func() bool { return c.Status().State == StateLapsed })
	if d := c.Status().Detail; d != "because the test said so" {
		t.Errorf("the status says %q, want the reason the relay closed with", d)
	}
	c.RetryNow()
	waitFor(t, "a second attempt", func() bool { return f.count() == 2 })
}

// The account's plan comes with the roster, its times by this machine's
// clock as every other time the relay sends is.
func TestTheRosterCarriesThePlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The relay's clock is an hour behind this machine's.
		w.Header().Set("Date", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"devices":[],"hosts":[],"plan":{"plan":"lapsed","name":"Lapsed","active":false,`+
			`"ends":"2030-01-01T00:00:00Z","was":"trial","deleteAt":"2030-04-01T00:00:00Z","message":"Your remote access trial has ended."}}`)
	}))
	defer srv.Close()
	roster, err := NewClient(&Config{Relay: srv.URL, Token: "fdh_test"}, "v").Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := roster.Plan
	if p == nil || p.Plan != "lapsed" || p.Active || p.Was != "trial" || p.Message != "Your remote access trial has ended." {
		t.Fatalf("the plan came back as %+v", p)
	}
	near := func(got time.Time, want string) bool {
		w, _ := time.Parse(time.RFC3339, want)
		d := got.Sub(w.Add(time.Hour))
		return d > -5*time.Second && d < 5*time.Second
	}
	if !near(p.Ends, "2030-01-01T00:00:00Z") || !near(p.DeleteAt, "2030-04-01T00:00:00Z") {
		t.Errorf("the plan's times are %s and %s, want them an hour on, by this machine's clock", p.Ends, p.DeleteAt)
	}
	// A relay with no plans sends none, and none is made up.
	f := newFakeRelay(t)
	roster, err = NewClient(&Config{Relay: f.URL, Token: f.token}, "v").Devices(context.Background())
	if err != nil || roster.Plan != nil {
		t.Errorf("a relay with no plans gave %+v, %v", roster, err)
	}
}
