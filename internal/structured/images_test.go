package structured

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func pngImage(n int) Image {
	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, n)...)
	return Image{Image: acp.Image{MediaType: "image/png", Data: data}, Width: 10, Height: 20, Upload: "1-shot.png"}
}

func TestAnImagePromptSendsBlocksAndTheEntryCarriesMetadata(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions()))
	until(t, r, "the agent to be ready", func(s Snapshot) bool { return s.Phase == PhaseReady })
	if err := r.SendImages("images", []Image{pngImage(30), pngImage(60)}); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "images=2") })
	if !strings.HasPrefix(strings.TrimSpace(replies(s)), "images=2 image/png:") {
		t.Errorf("reply = %q", replies(s))
	}
	var got []transcript.PromptImage
	for _, e := range s.Entries {
		if e.Kind == transcript.KindPrompt {
			got = e.Images
		}
	}
	if len(got) != 2 || got[0].MediaType != "image/png" || got[0].Bytes != 38 || got[1].Bytes != 68 || got[0].Width != 10 || got[0].Height != 20 || got[0].Upload != "1-shot.png" {
		t.Errorf("prompt image metadata = %+v", got)
	}
}

func TestAnImagePromptForAnAgentThatDoesNotTakeThemIsRefusedAndNothingIsSent(t *testing.T) {
	r := begin(t, fakeOptions(t))
	until(t, r, "the agent to be ready", func(s Snapshot) bool { return s.Phase == PhaseReady })
	err := r.SendImages("images", []Image{pngImage(5)})
	if !errors.Is(err, acp.ErrNoImages) {
		t.Fatalf("err = %v, want ErrNoImages", err)
	}
	if s := r.Snapshot(); s.Queued != 0 || len(s.Entries) != 0 {
		t.Errorf("something was queued or shown: queued %d, entries %d", s.Queued, len(s.Entries))
	}
}

func TestAnImagePromptRefusedWhenItComesUpShowsTheSentence(t *testing.T) {
	if got := failureText(acp.ErrNoImages, time.Now()); got != "This agent does not take images." {
		t.Errorf("failure text = %q", got)
	}
}

func TestImageLimitsAreEnforced(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions()))
	until(t, r, "the agent to be ready", func(s Snapshot) bool { return s.Phase == PhaseReady })
	var many []Image
	for i := 0; i <= MaxImages; i++ {
		many = append(many, pngImage(1))
	}
	if err := r.SendImages("images", many); err == nil {
		t.Error("more than MaxImages was taken")
	}
	if err := r.SendImages("images", []Image{pngImage(MaxImageBytes)}); err == nil {
		t.Error("an oversize image was taken")
	}
	if err := r.SendImages("images", []Image{{Image: acp.Image{MediaType: "image/png"}}}); err == nil {
		t.Error("an empty image was taken")
	}
	if r.Snapshot().Queued != 0 {
		t.Error("a refused prompt was queued")
	}
}

func TestAQueuedImagePromptShowsItsCountAndEditKeepsTheImages(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions()))
	slowTurn(t, r)
	if err := r.SendImages("look", []Image{pngImage(5), pngImage(6)}); err != nil {
		t.Fatal(err)
	}
	q := r.Snapshot().Queue
	if len(q) != 1 || q[0].Images != 2 || q[0].Text != "look" {
		t.Fatalf("queue = %+v", q)
	}
	if err := r.EditQueued(q[0].ID, "look again"); err != nil {
		t.Fatal(err)
	}
	if q = r.Snapshot().Queue; len(q) != 1 || q[0].Images != 2 || q[0].Text != "look again" {
		t.Errorf("after the edit: %+v", q)
	}
	// An image-only prompt can be queued, and its text can be emptied.
	if err := r.SendImages("", []Image{pngImage(5)}); err != nil {
		t.Fatalf("an image-only prompt: %v", err)
	}
	// A prompt of text alone cannot be emptied.
	if err := r.Send("words"); err != nil {
		t.Fatal(err)
	}
	if err := r.EditQueued(r.Snapshot().Queue[2].ID, ""); err == nil {
		t.Error("a text prompt was edited to nothing")
	}
	if err := r.EditQueued(q[0].ID, ""); err != nil {
		t.Errorf("an image prompt could not be edited to no text: %v", err)
	}
}

func TestImageByteCapsPerPromptAndPerQueue(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions()))
	slowTurn(t, r)
	// Four pictures of 3 MB are 12 MB: over the 8 MB a prompt carries, though each
	// is under the 5 MB one may be.
	var big []Image
	for i := 0; i < MaxImages; i++ {
		big = append(big, pngImage(3<<20))
	}
	if err := r.SendImages("big", big); !errors.Is(err, ErrImagesTooLarge) {
		t.Fatalf("err = %v, want ErrImagesTooLarge", err)
	}
	// Two prompts of 2 x 3 MB hold 12 MB of the 16 MB the queue takes, so a 5 MB one is refused
	// and the text-only ones are not.
	four := []Image{pngImage(3 << 20), pngImage(3 << 20)}
	for i := 0; i < 2; i++ {
		if err := r.SendImages("fits", four); err != nil {
			t.Fatalf("prompt %d: %v", i+1, err)
		}
	}
	if err := r.SendImages("one more", []Image{pngImage(MaxImageBytes - 100)}); !errors.Is(err, ErrQueueImagesFull) {
		t.Fatalf("err = %v, want ErrQueueImagesFull", err)
	}
	if err := r.Send("words"); err != nil {
		t.Errorf("a text prompt was refused by the picture cap: %v", err)
	}
	if n := r.Snapshot().Queued; n != 3 {
		t.Errorf("queued = %d, want 3", n)
	}
}
