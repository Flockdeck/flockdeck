package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/jev"
	"github.com/jmwri/flockdeck/internal/routejev"
)

// fakeJev stands in for TypeSafe for the whole path -- routeRows, the shared
// classifier, the real jev client -- and counts the requests that reach it.
func fakeJev(t *testing.T, score, confidence float64) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		out, _ := json.Marshal(map[string]any{"model": "jev-test", "answers": map[string]any{
			"difficulty": map[string]any{"type": "score", "score": score, "confidence": confidence},
			"mechanical": map[string]any{"type": "noul", "noul": 0.05},
			"multifile":  map[string]any{"type": "noul", "noul": 0.2},
		}})
		_, _ = w.Write(out)
	}))
	routejev.APIBase = srv.URL
	routejev.Reset()
	t.Cleanup(func() { srv.Close(); routejev.APIBase = ""; routejev.Reset() })
	return &n
}

const jevPolicy = `{"mode": "suggest", "strategy": "cost", "jev": true, "rules": []}`

func modelOfTier(t *testing.T, c *agent.Catalog, id, tier string) string {
	t.Helper()
	spec, ok := c.Find(id)
	if !ok {
		t.Fatalf("no agent %q", id)
	}
	for _, m := range spec.Models {
		if m.Tier == tier {
			return m.ID
		}
	}
	t.Fatalf("%s has no %s model", id, tier)
	return ""
}

// Anything that turns Jev off leaves routeRows exactly what it was, with no
// request made.
func TestWithoutBothSwitchesRouteRowsAreThePlainOnesAndNothingIsSent(t *testing.T) {
	tasks := []string{"add a health endpoint", "", "run the tests", "design the sync engine"}
	plainPolicy := `{"mode": "suggest", "strategy": "cost", "rules": []}`
	plain, _, _ := routeRows(catalogRouting(plainPolicy), "/p", "claude", "opus", tasks)
	if plain == nil || plain[0] == nil {
		t.Fatalf("the plain fallback routed nothing: %+v", plain)
	}
	cases := map[string]struct {
		key, policy string
	}{
		"no key":            {"", jevPolicy},
		"setting off":       {"k-test", plainPolicy},
		"balanced strategy": {"k-test", `{"mode": "suggest", "jev": true, "rules": []}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(jev.KeyEnv, tc.key)
			n := fakeJev(t, 4, 1)
			want := plain
			if name == "balanced strategy" {
				want, _, _ = routeRows(catalogRouting(`{"mode": "suggest", "rules": []}`), "/p", "claude", "opus", tasks)
			}
			got, _, _ := routeRows(catalogRouting(tc.policy), "/p", "claude", "opus", tasks)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("routes differ from the plain ones:\n got %+v\nwant %+v", got, want)
			}
			if n.Load() != 0 {
				t.Errorf("%d requests reached TypeSafe", n.Load())
			}
		})
	}
}

func TestARuleDecidedRowSendsNothingToJev(t *testing.T) {
	t.Setenv(jev.KeyEnv, "k-test")
	n := fakeJev(t, 4, 1)
	c := catalogRouting(`{"mode": "suggest", "strategy": "cost", "jev": true}`) // built-in rules
	routes, _, _ := routeRows(c, "/p", "claude", "opus", []string{"run the tests", "refactor the store"})
	if len(routes) != 2 || routes[0] == nil || routes[0].Rule != "run the tests" {
		t.Fatalf("routes = %+v", routes)
	}
	if n.Load() != 0 {
		t.Errorf("%d requests for rows a rule decided", n.Load())
	}
}

func TestSettingsShowsTheSwitchTheKeyAndTheComparison(t *testing.T) {
	t.Setenv(jev.KeyEnv, "")
	v := routingOf(catalogRouting(`{"mode": "suggest"}`), "")
	if v.Jev || v.JevKey {
		t.Errorf("Jev shown on by default: %+v", v)
	}
	t.Setenv(jev.KeyEnv, "k-test")
	v = routingOf(catalogRouting(jevPolicy), "")
	if !v.Jev || !v.JevKey {
		t.Errorf("view = %+v, want the setting and the key seen", v)
	}
	if got := routingNotice("every project", "jev", "true"); !strings.Contains(got, "TypeSafe") {
		t.Errorf("the notice for turning it on does not say where the text goes: %q", got)
	}
}
