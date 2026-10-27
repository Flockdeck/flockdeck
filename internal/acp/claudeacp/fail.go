package claudeacp

import (
	"encoding/json"
	"strings"
)

// Failure classes. A failed turn is told to the client as one of these, in the
// data of the error: {"class": "usage_limit", "resetsAt": <Unix seconds>}. The
// message is fixed per class. Nothing claude wrote in its result reaches the
// client; the class is read from structured fields only (the assistant line's
// error code, the result's api_error_status and terminal_reason, and the
// rate_limit_event line), never from text.
const (
	classUsageLimit = "usage_limit"
	classContext    = "context_full"
	classAuth       = "authentication"
	classOutage     = "outage"
	classRefused    = "refused"
	classOther      = "other"
)

var classMessages = map[string]string{
	classUsageLimit: "Usage limit reached",
	classContext:    "The conversation is too long for the model",
	classAuth:       "Authentication required",
	classOutage:     "The service is overloaded or unreachable",
	classRefused:    "The model declined this request",
	classOther:      "the turn failed",
}

// turnFacts is what claude said about the running turn before its result line:
// the error code of an API error message and a rejected rate limit with its
// reset time. It is kept per turn and cleared when a turn starts and ends.
type turnFacts struct {
	apiError string // "error" of a top-level assistant line, such as rate_limit
	rejected bool   // a rate_limit_event with status rejected
	resetsAt float64
}

// rateLimitInfo is the rate_limit_info of a rate_limit_event line.
type rateLimitInfo struct {
	Status   string  `json:"status"`
	ResetsAt float64 `json:"resetsAt"`
}

// errString reads a line's "error" member when it is a string.
func errString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil || len(s) > 64 {
		return ""
	}
	return s
}

// failure builds the error for a result that says the turn failed. The class
// comes from facts and the result's own fields, in this order: context, login,
// limit, outage, a rejected limit event, refusal, other.
func failure(l line, f turnFacts) *rpcErr {
	status := l.APIErrorStatus
	class := classOther
	switch {
	case l.TerminalReas == "prompt_too_long" || l.TerminalReas == "blocking_limit" ||
		l.TerminalReas == "rapid_refill_breaker" || status == 413:
		class = classContext
	case isAuthError(f.apiError) || status == 401:
		class = classAuth
	case f.apiError == "rate_limit" || f.apiError == "billing_error" || status == 429:
		class = classUsageLimit
	case f.apiError == "server_error" || status >= 500 || status == 408:
		class = classOutage
	case f.rejected:
		// claude refused the turn for a limit and sent no error code of its own.
		class = classUsageLimit
	case l.StopReason == "refusal":
		class = classRefused
	case legacyAuthText(l.Result):
		// claude versions that wrote no error code: the result text is all there
		// is. It only ever selects the login prompt, which asks the person to
		// sign in and does nothing else. Every structured class above wins over it.
		class = classAuth
	}
	if class == classAuth {
		return &rpcErr{Code: codeAuthRequired, Message: classMessages[classAuth]}
	}
	data := map[string]any{"class": class}
	if class == classUsageLimit && f.resetsAt > 0 {
		data["resetsAt"] = f.resetsAt
	}
	return &rpcErr{Code: codeInternal, Message: classMessages[class], Data: data}
}

func isAuthError(code string) bool {
	switch code {
	case "authentication_failed", "account_on_hold", "oauth_org_not_allowed", "verification_required":
		return true
	}
	return false
}

func legacyAuthText(result string) bool {
	low := strings.ToLower(result)
	return strings.Contains(low, "/login") || strings.Contains(low, "not logged in") || strings.Contains(low, "invalid api key")
}
