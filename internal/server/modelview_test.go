package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

func builtin(t *testing.T, id string) agent.Spec {
	t.Helper()
	spec, ok := agent.Merge(nil).Find(id)
	if !ok {
		t.Fatalf("no built-in %q", id)
	}
	return spec
}

// A price is shown beside a model only where it is what the user pays: an API
// agent talking to its vendor's own endpoint. A command-line agent may be on a
// subscription, and a gateway charges what it charges.
func TestOnlyAnAPIAgentOnItsVendorsEndpointShowsPrices(t *testing.T) {
	api := modelViews(builtin(t, "anthropic"))
	for _, v := range api {
		if v.Price == nil || v.Price.Checked == "" {
			t.Errorf("%s is shown without a dated price", v.ID)
		}
	}
	for _, v := range modelViews(builtin(t, "claude")) {
		if v.Price != nil {
			t.Errorf("Claude Code's %q is shown with a per-token price", v.ID)
		}
	}
	proxied := builtin(t, "anthropic")
	proxied.API.BaseURL = "http://127.0.0.1:8080/v1"
	for _, v := range modelViews(proxied) {
		if v.Price != nil {
			t.Errorf("%s through a gateway is shown with the vendor's price", v.ID)
		}
	}

	// The window reads the model's own fields where it always has, with the
	// tier and price beside them.
	data, err := json.Marshal(api[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"claude-sonnet-5"`, `"tier":"mid"`, `"price":{"in":`, `"checked":"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not carry %s", data, want)
		}
	}
}
