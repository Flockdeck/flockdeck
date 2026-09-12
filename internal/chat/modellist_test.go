package chat

import (
	"fmt"
	"strings"
	"testing"
)

// An endpoint that offers hundreds of models is listed a screenful at a time,
// with the one answering always among them, and the rest a name away.
func TestModelListIsAScreenfulAtATime(t *testing.T) {
	var models []ModelChoice
	for i := 1; i <= 100; i++ {
		models = append(models, ModelChoice{ID: fmt.Sprintf("vendor/model-%03d", i)})
	}
	out := run(t, Options{Agent: "gateway", Model: "vendor/model-077", Models: models},
		"/model\n/model model-0\n/exit\n", &scriptedWire{})

	if !strings.Contains(out, "vendor/model-040") || strings.Contains(out, "vendor/model-041") {
		t.Errorf("the listing is not cut at 40:\n%s", out)
	}
	if !strings.Contains(out, "* 77  vendor/model-077") {
		t.Errorf("the model answering is not listed:\n%s", out)
	}
	if !strings.Contains(out, "(and 59 more; /model <part of a name> lists the ones that match)") {
		t.Errorf("the listing does not say how many more there are:\n%s", out)
	}
	if !strings.Contains(out, "(and 59 more; more of the name narrows it)") {
		t.Errorf("the matches are not cut at 40:\n%s", out)
	}
}
