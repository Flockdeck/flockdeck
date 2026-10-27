package apikey

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckKeyReportsTheStatusAndNeverTheVendorsWords(t *testing.T) {
	const key = "sk-test-abcdefghijkl"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		wire   string
		refuse bool
		ok     bool
	}{
		{"taken", 200, `{"data":[]}`, "openai", false, true},
		{"refused", 401, `Incorrect API key provided: ` + key, "openai", true, false},
		{"gemini refusal is a 400", 400, `{"error":{"message":"API key not valid. ` + key + `"}}`, "gemini", true, false},
		{"another 400 is not a refusal", 400, `bad request ` + key, "anthropic", false, false},
		{"forbidden is not a refusal", 403, `nope ` + key, "anthropic", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			err := CheckKey(context.Background(), tc.wire, srv.URL, key)
			switch {
			case tc.ok && err != nil:
				t.Fatalf("a taken key gave %v", err)
			case !tc.ok && err == nil:
				t.Fatal("a failing answer gave nil")
			}
			if errors.Is(err, ErrKeyRefused) != tc.refuse {
				t.Errorf("refused = %v, want %v (err %v)", errors.Is(err, ErrKeyRefused), tc.refuse, err)
			}
			if err != nil && strings.Contains(err.Error(), "abcdefghijkl") {
				t.Errorf("the error quotes the key: %v", err)
			}
		})
	}
}

func TestCheckKeySendsTheKeyInTheWiresOwnHeader(t *testing.T) {
	for wire, header := range map[string]string{"anthropic": "x-api-key", "gemini": "x-goog-api-key", "openai": "Authorization"} {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get(header)
		}))
		if err := CheckKey(context.Background(), wire, srv.URL, "sk-k"); err != nil {
			t.Errorf("%s: %v", wire, err)
		}
		srv.Close()
		if !strings.Contains(got, "sk-k") {
			t.Errorf("%s sent %q in %s, want the key", wire, got, header)
		}
	}
}

func TestCheckKeyDoesNotFollowARedirectToAnotherHost(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("x-api-key") != ""
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()
	// Two httptest servers are on the same host and different ports: the host
	// compared includes the port.
	if err := CheckKey(context.Background(), "anthropic", srv.URL, "sk-k"); err == nil {
		t.Error("a redirect elsewhere was followed")
	}
	if leaked {
		t.Error("the key reached the other address")
	}
}

func TestCheckKeyFindsAnEndpointNobodyListensAt(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	if err := CheckKey(context.Background(), "openai", "http://"+addr, "sk-k"); !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestCheckKeyRefusesAWireItDoesNotKnow(t *testing.T) {
	if err := CheckKey(context.Background(), "carrier-pigeon", "", "sk-k"); err == nil {
		t.Error("an unknown wire was accepted")
	}
}

func TestKeyForGoesByEnvironmentThenStoreThenShared(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("FLOCKDECK_API_KEY", "")
	saved := KeyStore
	t.Cleanup(func() { KeyStore = saved })
	KeyStore = func(id string) string {
		if id == "anthropic" {
			return "sk-stored"
		}
		return ""
	}
	target := Target{Agent: "anthropic", Wire: "anthropic", KeyEnv: []string{"ANTHROPIC_API_KEY"}}
	if k, _ := KeyFor(target); k != "sk-stored" {
		t.Errorf("with only a stored key got %q", k)
	}
	t.Setenv("FLOCKDECK_API_KEY", "sk-shared")
	if k, _ := KeyFor(target); k != "sk-stored" {
		t.Errorf("the shared variable beat the store: %q", k)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-env")
	k, from := KeyFor(target)
	if k != "sk-env" || strings.Contains(from, "sk-env") {
		t.Errorf("got %q from %q, want the variable and a source that does not name the key", k, from)
	}
}

func TestKeyForDoesNotSendTheVendorsVariableToAGateway(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-vendor")
	t.Setenv("FLOCKDECK_API_KEY", "")
	if k, _ := KeyFor(Target{Wire: "openai", BaseURL: "https://gw.example/v1"}); k != "" {
		t.Errorf("a gateway was given %q", k)
	}
	if k, _ := KeyFor(Target{Wire: "openai"}); k != "sk-vendor" {
		t.Errorf("the vendor itself got %q", k)
	}
}

func TestAnEndpointOnThisMachineNeedsNoKey(t *testing.T) {
	t.Setenv("FLOCKDECK_API_KEY", "")
	k, from := KeyFor(Target{Wire: "openai", BaseURL: "http://LocalHost:11434/v1"})
	if k != "" || from == "" {
		t.Errorf("got %q, %q; want no key and a reason", k, from)
	}
}
