package bridge

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func pngB64(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func imgBlock(mt, data string) *acp.ContentBlock {
	return &acp.ContentBlock{Type: "image", MimeType: mt, Data: data}
}

func imgContent(mt, data string) acp.ToolContent {
	return acp.ToolContent{Type: "content", Content: imgBlock(mt, data)}
}

func imageEntries(b *Bridge) []transcript.Entry {
	var out []transcript.Entry
	for _, e := range b.Snapshot() {
		if e.Kind == transcript.KindImage {
			out = append(out, e)
		}
	}
	return out
}

func TestAToolResultPictureIsAnImageEntryWithDetail(t *testing.T) {
	b, _ := newBridge(Config{})
	data := pngB64(t, 3, 2)
	b.Update(toolCall("c1", "read", "Read a.png"))
	b.Update(toolUpdate("c1", "completed", outText("ok"), imgContent("image/png", data)))
	imgs := imageEntries(b)
	if len(imgs) != 1 {
		t.Fatalf("got %d image entries, want 1: %v", len(imgs), kinds(b.Snapshot()))
	}
	e := imgs[0]
	if e.MediaType != "image/png" || e.Width != 3 || e.Height != 2 || !e.HasDetail || e.Bytes == 0 {
		t.Errorf("entry = %+v", e)
	}
	d, ok := b.Detail(e.ID)
	if !ok || d.Data != data {
		t.Errorf("detail ok=%v, data matches=%v", ok, d.Data == data)
	}
}

func TestARepeatedToolUpdateDoesNotMakeThePictureAgain(t *testing.T) {
	b, _ := newBridge(Config{})
	data := pngB64(t, 1, 1)
	b.Update(toolCall("c1", "read", "Read"))
	for i := 0; i < 3; i++ {
		b.Update(toolUpdate("c1", "in_progress", imgContent("image/png", data)))
	}
	if n := len(imageEntries(b)); n != 1 {
		t.Errorf("got %d image entries, want 1", n)
	}
}

func TestOnePicturePerToolIsBoundedByFour(t *testing.T) {
	b, _ := newBridge(Config{})
	b.Update(toolCall("c1", "read", "Read"))
	for i := 1; i <= 6; i++ {
		b.Update(toolUpdate("c1", "in_progress", imgContent("image/png", pngB64(t, i, 1))))
	}
	if n := len(imageEntries(b)); n != maxImagesPerTool {
		t.Errorf("got %d image entries, want %d", n, maxImagesPerTool)
	}
}

func TestAReplyPictureIsAnImageEntryBetweenTheReplyText(t *testing.T) {
	b, _ := newBridge(Config{})
	b.Update(text(acp.UpdateAgentMessageChunk, "before"))
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/jpeg", pngB64(t, 2, 2))})
	b.Update(text(acp.UpdateAgentMessageChunk, "after"))
	if got := kinds(b.Snapshot()); got != "reply,image,reply" {
		t.Errorf("kinds = %s, want reply,image,reply", got)
	}
}

func TestAnUntrustedPictureIsRefused(t *testing.T) {
	good := pngB64(t, 2, 2)
	huge := base64.StdEncoding.EncodeToString(make([]byte, maxImageBytes+1))
	cases := map[string]*acp.ContentBlock{
		"svg":            imgBlock("image/svg+xml", good),
		"html":           imgBlock("text/html", good),
		"no type":        imgBlock("", good),
		"no data":        imgBlock("image/png", ""),
		"not base64":     imgBlock("image/png", "data:image/png;base64,"+good),
		"newline":        imgBlock("image/png", good[:8]+"\n"+good[8:]),
		"padding inside": imgBlock("image/png", "AA==AAAA"),
		"too large":      imgBlock("image/png", huge),
		"short padding":  imgBlock("image/png", good+"="),
	}
	for name, blk := range cases {
		b, _ := newBridge(Config{})
		b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: blk})
		if n := len(b.Snapshot()); n != 0 {
			t.Errorf("%s: %d entries made, want none", name, n)
		}
	}
	if !base64Clean(strings.Repeat("A", 8)) || base64Clean("AA=A") {
		t.Error("base64Clean")
	}
}

func TestImageEntriesPerSessionAreBounded(t *testing.T) {
	b, _ := newBridge(Config{})
	for i := 0; i < maxImages+20; i++ {
		b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, 1, 1))})
	}
	if n := len(imageEntries(b)); n != maxImages {
		t.Errorf("got %d image entries, want %d", n, maxImages)
	}
}

func TestPictureDetailIsTrimmedWithTheBudgetAndTheEntryStays(t *testing.T) {
	b, _ := newBridge(Config{DetailBudget: 100})
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, 40, 40))})
	e := imageEntries(b)
	if len(e) != 1 || e[0].HasDetail {
		t.Fatalf("entries = %+v", e)
	}
	if _, ok := b.Detail(e[0].ID); ok {
		t.Error("detail kept past the budget")
	}
}

// countImages is what b.images should be: the image entries held.
func countImages(b *Bridge) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, it := range b.items {
		if it.e.Kind == transcript.KindImage {
			n++
		}
	}
	return n
}

func TestTheImageCountFollowsAReplay(t *testing.T) {
	b, _ := newBridge(Config{})
	for i := 0; i < maxImages; i++ {
		b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, 1, 1))})
	}
	b.BeginReplay()
	if b.images != 0 {
		t.Fatalf("count after a reset = %d", b.images)
	}
	b.Update(replayed(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, 2, 2))}))
	b.EndReplay()
	if n := len(imageEntries(b)); n != 1 || b.images != 1 {
		t.Errorf("entries = %d, count = %d, want 1 and 1", n, b.images)
	}
}

func TestTheImageCountFollowsATrimmedReplay(t *testing.T) {
	b, _ := newBridge(Config{MaxEntries: 20})
	b.BeginReplay()
	for i := 0; i < 80; i++ {
		b.Update(replayed(text(acp.UpdateAgentMessageChunk, "a")))
		b.Update(replayed(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, i%7+1, 1))}))
	}
	b.EndReplay()
	if b.Dropped() == 0 {
		t.Fatal("the replay was not trimmed")
	}
	if got, want := b.images, countImages(b); got != want {
		t.Errorf("count = %d, images held = %d", got, want)
	}
}

func TestTheImageCountFollowsARewind(t *testing.T) {
	b, _ := newBridge(Config{})
	id, _ := b.StartTurn("draw")
	for i := 0; i < 3; i++ {
		b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, i+1, 1))})
	}
	if b.images != 3 {
		t.Fatalf("count = %d", b.images)
	}
	if _, ok := b.TruncateBefore(id); !ok {
		t.Fatal("no rewind")
	}
	if b.images != 0 || countImages(b) != 0 {
		t.Errorf("count = %d, held = %d, want none", b.images, countImages(b))
	}
	// Rewinding over and over does not use up the allowance.
	for round := 0; round < maxImages; round++ {
		id, _ = b.StartTurn("again")
		b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, 1, 1))})
		b.TruncateBefore(id)
	}
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: imgBlock("image/png", pngB64(t, 3, 3))})
	if n := len(imageEntries(b)); n != 1 {
		t.Errorf("after %d rewinds a picture made %d entries, want 1", maxImages, n)
	}
}

func TestARepeatedToolUpdateKeepsTheCountAndTheEntry(t *testing.T) {
	b, _ := newBridge(Config{})
	data := pngB64(t, 1, 1)
	b.Update(toolCall("c1", "read", "Read"))
	// The same picture twice in one update, then again in later updates and the completion.
	b.Update(toolUpdate("c1", "in_progress", imgContent("image/png", data), imgContent("image/png", data)))
	b.Update(toolUpdate("c1", "in_progress", imgContent("image/png", data)))
	b.Update(toolUpdate("c1", "completed", outText("ok"), imgContent("image/png", data)))
	if n := len(imageEntries(b)); n != 1 || b.images != 1 {
		t.Errorf("entries = %d, count = %d, want 1 and 1", n, b.images)
	}
}
