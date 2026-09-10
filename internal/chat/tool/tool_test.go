package tool

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestSetIsTheV1Tools pins the list in section 8 of the design, in the order
// the model is told about them.
func TestSetIsTheV1Tools(t *testing.T) {
	set, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"read_file", "write_file", "edit_file", "list_dir", "glob", "grep", "run_command"}
	var got []string
	for _, tl := range set.Tools() {
		got = append(got, tl.Name())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tools are %q, want %q", got, want)
	}
	for _, name := range want {
		if _, ok := set.Lookup(name); !ok {
			t.Errorf("Lookup(%q) found nothing", name)
		}
	}
	if _, ok := set.Lookup("rm_rf"); ok {
		t.Error("Lookup found a tool that does not exist")
	}
}

// TestSchemasAreWellFormed guards what every wire format needs from a schema
// before it can translate it: a name, something to tell the model, an object at
// the top, and required arguments that actually exist.
func TestSchemasAreWellFormed(t *testing.T) {
	set, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range set.Schemas() {
		t.Run(s.Name, func(t *testing.T) {
			if s.Name == "" || s.Description == "" {
				t.Fatalf("schema is incomplete: %+v", s)
			}
			if s.Params.Type != "object" {
				t.Errorf("top level is %q, want object", s.Params.Type)
			}
			for _, req := range s.Params.Required {
				if _, ok := s.Params.Properties[req]; !ok {
					t.Errorf("%q is required but not described", req)
				}
			}
			for name, p := range s.Params.Properties {
				if p.Type == "" || p.Description == "" {
					t.Errorf("property %q is incomplete: %+v", name, p)
				}
			}
			if _, err := json.Marshal(s); err != nil {
				t.Errorf("schema does not survive JSON: %v", err)
			}
		})
	}
}

// TestSetSharesOneAllowlist matters because the standing permission the user
// gives is for the session, and a tool holding its own copy would ask again.
func TestSetSharesOneAllowlist(t *testing.T) {
	set, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd, ok := set.Lookup("run_command")
	if !ok {
		t.Fatal("no run_command")
	}
	pa, ok := cmd.(PrefixApprover)
	if !ok {
		t.Fatal("run_command should offer standing permission")
	}
	pa.Allow("git status")
	if !set.Allowlist().Allowed("git status") {
		t.Error("the set and the tool should share one allowlist")
	}
}

func TestNewRefusesAWorkingDirectoryThatIsNotThere(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("a set with no working directory has nothing to confine the tools to")
	}
}
