package workspace

import "testing"

// A later key that differs from env only in case, or env twice, or a name set twice, must not
// take back a gateway: Go's decoder would, so these files are read key by key.
func TestASettingsFileCannotTakeBackAGatewayWithAnotherSpellingOfEnv(t *testing.T) {
	gw := `"env":{"ANTHROPIC_BASE_URL":"https://gw.example"}`
	for name, body := range map[string]string{
		"ENV after":          `{` + gw + `,"ENV":{}}`,
		"Env null after":     `{` + gw + `,"Env":null}`,
		"env before":         `{"eNv":{},` + gw + `}`,
		"env twice":          `{` + gw + `,"env":{}}`,
		"name twice":         `{"env":{"ANTHROPIC_BASE_URL":"https://gw.example","ANTHROPIC_BASE_URL":""}}`,
		"name in lower case": `{"env":{"anthropic_base_url":"https://gw.example"}}`,
		"name mixed case":    `{"env":{"Anthropic_Base_Url":"https://gw.example"}}`,
	} {
		isolateEnv(t)
		repo, claude := repoWith(t)
		writeSettings(t, claude, []byte(body))
		if p, why := BatonProviderDetail(claudeSpec, repo); p == "anthropic" {
			t.Errorf("%s: %s was taken for the vendor (%s)", name, body, why)
		}
	}
	// A file that says nothing wrong is still known.
	isolateEnv(t)
	repo, claude := repoWith(t)
	writeSettings(t, claude, []byte(`{"model":"x","env":{"FOO":"bar"}}`))
	if p, why := BatonProviderDetail(claudeSpec, repo); p != "anthropic" {
		t.Errorf("a plain file: %q, %q", p, why)
	}
}

// The process environment is read by name in any case as well.
func TestAnEnvironmentVariableInAnotherCaseIsSeen(t *testing.T) {
	isolateEnv(t)
	t.Setenv("anthropic_base_url", "https://gw.example")
	if p, _ := BatonProviderDetail(claudeSpec, ""); p == "anthropic" {
		t.Error("a lower case variable was not seen")
	}
}

// lookupFold is a lookup by name in any case, on every system (the process environment is
// already case-insensitive on Windows, which hides the fold there).
func TestLookupFoldFindsANameInAnyCase(t *testing.T) {
	m := map[string]string{"anthropic_base_url": "https://gw.example", "Other": ""}
	if got := lookupFold(m, "ANTHROPIC_BASE_URL"); got != "https://gw.example" {
		t.Errorf("lookupFold = %q", got)
	}
	if got := lookupFold(map[string]string{"A": "", "a": "x"}, "A"); got != "x" {
		t.Errorf("a value that says something is preferred: %q", got)
	}
	if got := lookupFold(m, "NOPE"); got != "" {
		t.Errorf("a name that is not there: %q", got)
	}
}

// An env under another spelling of the key, alone, is not read as env: the file is unreadable.
func TestALoneEnvInAnotherCaseMakesTheFileUnreadable(t *testing.T) {
	for _, body := range []string{`{"ENV":{"ANTHROPIC_BASE_URL":"https://gw.example"}}`, `{"Env":{"ANTHROPIC_BASE_URL":"https://gw.example"}}`, `{"eNV":{}}`} {
		if _, ok := parseSettingsEnv([]byte(body)); ok {
			t.Errorf("%s was read", body)
		}
		isolateEnv(t)
		repo, claude := repoWith(t)
		writeSettings(t, claude, []byte(body))
		if p, _ := BatonProviderDetail(claudeSpec, repo); p == "anthropic" {
			t.Errorf("%s: taken for the vendor", body)
		}
	}
	if env, ok := parseSettingsEnv([]byte(`{"env":{"A":"b"}}`)); !ok || env["A"] != "b" {
		t.Errorf("a plain env: %v %v", env, ok)
	}
}
