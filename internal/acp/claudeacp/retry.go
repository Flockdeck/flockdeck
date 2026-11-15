package claudeacp

import (
	"bytes"
	"encoding/json"
)

// Retry classes sent in a flockdeck_notice update with the code api_retry. The
// set is closed: the bridge words each one itself and treats anything else as
// "other". claude's own error text is never passed on.
const (
	retryOverloaded = "overloaded"
	retryRateLimit  = "rate_limit"
	retryServer     = "server_error"
	retryNetwork    = "network"
	retryOther      = "other"
)

// maxRetryCount bounds the attempt numbers passed on.
const maxRetryCount = 999

// handleAPIRetry turns claude's system/api_retry line (a request failed and will
// be retried after a delay) into a flockdeck_notice update. It carries the
// attempt numbers and a class, nothing else of the line.
func (ss *session) handleAPIRetry(l line) {
	attempt, ok1 := smallInt(l.RetryAttempt)
	limit, ok2 := smallInt(l.RetryMax)
	if !ok1 || !ok2 || attempt < 1 || limit < 1 || attempt > limit {
		return
	}
	ss.srv.update(ss.id, map[string]any{
		"sessionUpdate": "flockdeck_notice",
		"code":          "api_retry",
		"attempt":       attempt,
		"max":           limit,
		"class":         retryClass(errString(l.Error), l.RetryStatus, l.RetryNoResponse),
	})
}

// retryClass reads claude's error code. A request with no HTTP status (a
// timeout, a dropped connection) is a network problem whatever the code says.
func retryClass(code string, status, noResponse json.RawMessage) string {
	switch code {
	case "overloaded":
		return retryOverloaded
	case "rate_limit":
		return retryRateLimit
	case "server_error":
		return retryServer
	}
	if len(bytes.TrimSpace(noResponse)) > 0 && string(bytes.TrimSpace(noResponse)) != "null" {
		return retryNetwork
	}
	if s := bytes.TrimSpace(status); len(s) == 0 || string(s) == "null" {
		if code == "unknown" || code == "" {
			return retryNetwork
		}
	}
	return retryOther
}

// smallInt reads a JSON whole number from 0 to maxRetryCount.
func smallInt(raw json.RawMessage) (int, bool) {
	var n int
	if json.Unmarshal(raw, &n) != nil || n < 0 || n > maxRetryCount {
		return 0, false
	}
	return n, true
}
