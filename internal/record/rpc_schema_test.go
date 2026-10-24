package record

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/record/schemacheck"
)

const (
	rpcSchemaPath = "../../api/schema/rpc-recording-line.schema.json"
	rpcDocPath    = "../../docs/rpc-recording-format.md"
)

func loadRPCSchema(t *testing.T) schemacheck.Schema {
	t.Helper()
	s, err := schemacheck.Load(rpcSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Every line the recorder writes, of every type, with coalescing, redaction,
// clipping and the cap in play, validates against the published schema.
func TestRPCEmittedLinesMatchTheSchema(t *testing.T) {
	schema := loadRPCSchema(t)
	r, _, _ := newRPC(t, RPCConfig{MaxBytes: 12 << 10})
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "hello"))
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", " ghp_"+strings.Repeat("a1B2", 9)))
	r.Tap(RPCIn, toolUpd("s1", "c1", "completed", `,"content":[{"type":"content","content":{"type":"text","text":"`+strings.Repeat("x", MaxFieldBytes*2)+`"}}]`))
	r.Tap(RPCIn, []byte("not json"))
	r.Tap(RPCIn, []byte(`{"method":"x","params":"`+strings.Repeat("y", rpcMaxRaw)+`"}`))
	for i := 0; i < 200; i++ {
		r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":2,"method":"m","params":{"pad":"`+strings.Repeat("z", 100)+`"}}`))
	}
	r.Gap(2)
	f := read(t, r)
	raw, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if errs := schema.Validate([]byte(line)); len(errs) != 0 {
			t.Errorf("line does not match the schema: %v\n%s", errs, line)
		}
		var l RPCLine
		_ = json.Unmarshal([]byte(line), &l)
		seen[l.Type] = true
	}
	for _, ty := range []string{TypeRPCStarted, TypeRPCMessage, TypeRPCDropped, TypeRPCGap, TypeRPCStopped} {
		if !seen[ty] {
			t.Errorf("the test never emitted a %s line", ty)
		}
	}
	if f.Damaged != 0 {
		t.Errorf("damaged = %d", f.Damaged)
	}
	// The schema is strict: a field it does not name is refused.
	if errs := schema.Validate([]byte(`{"v":1,"seq":1,"time":"2026-10-19T12:00:00Z","session":"20261019T120000Z-abc.rpc","type":"rpc_stopped","text":"x","extra":1}`)); len(errs) == 0 {
		t.Error("an unknown field was accepted")
	}
	if errs := schema.Validate([]byte(`{"v":1,"seq":1,"time":"2026-10-19T12:00:00Z","session":"20261019T120000Z-abc.rpc","type":"rpc_message"}`)); len(errs) == 0 {
		t.Error("a message with no dir was accepted")
	}
}

// The schema names exactly the fields RPCLine has, and this page names them
// too, so none of the three can change without the others.
func TestRPCSchemaAndPageNameTheFieldsAndTypes(t *testing.T) {
	var s map[string]any
	b, err := os.ReadFile(rpcSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	props, _ := s["properties"].(map[string]any)
	var inSchema, inCode []string
	for k := range props {
		inSchema = append(inSchema, k)
	}
	rt := reflect.TypeOf(RPCLine{})
	for i := 0; i < rt.NumField(); i++ {
		inCode = append(inCode, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(inSchema)
	sort.Strings(inCode)
	if !reflect.DeepEqual(inSchema, inCode) {
		t.Errorf("schema fields %v, code fields %v", inSchema, inCode)
	}
	doc, err := os.ReadFile(rpcDocPath)
	if err != nil {
		t.Fatal(err)
	}
	page := string(doc)
	for _, f := range inCode {
		if !strings.Contains(page, "`"+f+"`") {
			t.Errorf("the page never names the field %q", f)
		}
	}
	for _, ty := range []string{TypeRPCStarted, TypeRPCMessage, TypeRPCDropped, TypeRPCGap, TypeRPCStopped} {
		if !strings.Contains(page, "`"+ty+"`") || !strings.Contains(string(b), `"`+ty+`"`) {
			t.Errorf("the type %s is missing from the page or the schema", ty)
		}
	}
	if !strings.Contains(page, "**1**") || RPCVersion != 1 {
		t.Error("the page and RPCVersion disagree about the version")
	}
}

// Every example on the page validates.
func TestRPCPageExamplesMatchTheSchema(t *testing.T) {
	schema := loadRPCSchema(t)
	doc, err := os.ReadFile(rpcDocPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\n(.*?)```").FindAllStringSubmatch(string(doc), -1)
	if len(blocks) == 0 {
		t.Fatal("no json examples on the page")
	}
	n := 0
	for _, bl := range blocks {
		for _, line := range strings.Split(strings.TrimSpace(bl[1]), "\n") {
			n++
			if errs := schema.Validate([]byte(line)); len(errs) != 0 {
				t.Errorf("example does not match: %v\n%s", errs, line)
			}
		}
	}
	if n < 3 {
		t.Errorf("only %d example lines", n)
	}
}
