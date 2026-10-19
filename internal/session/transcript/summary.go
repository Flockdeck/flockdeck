package transcript

// SummarizeToolOutput turns a tool's output into the one-line summary an
// entry carries, and says whether the full text is worth fetching as detail.
// It is the rule every adapter follows, for an agent whose own transcript is
// not read (the ACP bridge uses it).
func SummarizeToolOutput(text string) (summary string, hasDetail bool) {
	return summarizeToolOutput(text)
}

// DiffSummary stands in for a diff too large to send inline.
func DiffSummary(diff string) string { return diffSummary(diff) }

// OneLine is the first line of text, cut to max bytes with an ellipsis.
func OneLine(text string, max int) string { return oneLine(text, max) }
