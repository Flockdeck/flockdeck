package chat

import "testing"

// Gemini caches a long prompt unasked as well, bills the cached part at a
// tenth of the price, and says how much of the prompt it was in a count of its
// own.
func TestGeminiCountsTheCachedPartOfThePrompt(t *testing.T) {
	u := streamUsage(t, func(base string) Wire { return &geminiWire{base: base} },
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}],"+
			"\"usageMetadata\":{\"promptTokenCount\":10000,\"candidatesTokenCount\":5,\"cachedContentTokenCount\":9000}}\n\n")
	if u.In != 10000 || u.CacheRead != 9000 {
		t.Errorf("usage = %+v, want 10000 in of which 9000 read from the cache", u)
	}
}
