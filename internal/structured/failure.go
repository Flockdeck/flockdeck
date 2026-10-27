package structured

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
)

// Failure classes an adapter may name in the data of a prompt error
// ({"class": "usage_limit"}). Anything else is the generic text: what the agent
// wrote in the error is never shown.
const (
	ClassUsageLimit = "usage_limit"
	ClassContext    = "context_full"
	ClassOutage     = "outage"
	ClassRefused    = "refused"
)

var failureTexts = map[string]string{
	ClassUsageLimit: "Usage limit reached",
	ClassContext:    "The conversation is too long for the model; start a new conversation or compact it",
	ClassOutage:     "The service is overloaded or unreachable; try again shortly",
	ClassRefused:    "The model declined this request",
}

// resetWindow bounds a limit's reset time: one that is already past, or more
// than a month away, is not shown.
const resetWindow = 31 * 24 * time.Hour

// failureText is the one line a failed turn shows for err. The class comes from
// the error's data and must be one of ours; a usage limit adds its reset time
// only when the data holds one as a Unix time inside resetWindow, written in
// Flockdeck's own format and the local zone.
func failureText(err error, now time.Time) string {
	var rpc *acp.RPCError
	if !errors.As(err, &rpc) || len(rpc.Data) == 0 || len(rpc.Data) > 512 {
		return agentError
	}
	var d struct {
		Class    string  `json:"class"`
		ResetsAt float64 `json:"resetsAt"`
	}
	if json.Unmarshal(rpc.Data, &d) != nil {
		return agentError
	}
	text, ok := failureTexts[d.Class]
	if !ok {
		return agentError
	}
	if d.Class == ClassUsageLimit && d.ResetsAt > 0 && d.ResetsAt < 1e12 {
		at := time.Unix(int64(d.ResetsAt), 0).In(now.Location())
		if at.After(now) && at.Sub(now) <= resetWindow {
			layout := "15:04"
			if at.YearDay() != now.YearDay() || at.Year() != now.Year() {
				layout = "Mon 2 Jan 15:04"
			}
			return fmt.Sprintf("%s, resets at %s", text, at.Format(layout))
		}
	}
	return text
}
