package acp

import (
	"encoding/base64"
	"errors"
	"strconv"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

func TestPromptSendsImageBlocksWhenTheAgentTakesThem(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r, acptest.WithConfigOptions())
	if !c.Capabilities().Prompt.Image {
		t.Fatal("the fake agent in this shape should claim image support")
	}
	data := []byte("\x89PNG\r\n\x1a\nxyz")
	stop, err := c.PromptTurnImages(ctxT(t), sid, "", []string{"images"}, []Image{{MediaType: "image/png", Data: data}, {MediaType: "image/jpeg", Data: []byte("\xff\xd8\xffabc")}})
	if err != nil || stop != StopEndTurn {
		t.Fatalf("stop %q err %v", stop, err)
	}
	n := base64.StdEncoding.EncodedLen(len(data))
	want := "images=2 image/png:" + strconv.Itoa(n) + ",image/jpeg:" + strconv.Itoa(base64.StdEncoding.EncodedLen(6))
	if got := r.text(); got != want {
		t.Errorf("the agent saw %q, want %q", got, want)
	}
}

func TestPromptWithImagesIsRefusedWhenTheAgentDoesNotTakeThem(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	if c.Capabilities().Prompt.Image {
		t.Fatal("the default fake agent should not claim image support")
	}
	_, err := c.PromptTurnImages(ctxT(t), sid, "", []string{"images"}, []Image{{MediaType: "image/png", Data: []byte("x")}})
	if !errors.Is(err, ErrNoImages) {
		t.Fatalf("err = %v, want ErrNoImages", err)
	}
	if got := r.text(); got != "" {
		t.Errorf("the agent was sent a prompt anyway and answered %q", got)
	}
}

func TestPromptWithoutImagesIsUnchanged(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r, acptest.WithConfigOptions())
	if stop := prompt(t, c, sid, "images"); stop != StopEndTurn {
		t.Fatalf("stop %q", stop)
	}
	if got := r.text(); got != "images=0 " {
		t.Errorf("the agent saw %q", got)
	}
}
