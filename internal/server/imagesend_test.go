package server

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/structured"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeUpload(t *testing.T, root, pane, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(root, pane)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPaneImagesReadsAnUploadOfThePane(t *testing.T) {
	root := t.TempDir()
	p := writeUpload(t, root, "pane-a", "1-shot.png", testPNG(t, 3, 2))
	got, err := loadPaneImages(root, "pane-a", []imageRef{{Path: p}, {Path: p}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d images, want 1 (the same file twice is sent once)", len(got))
	}
	im := got[0]
	if im.MediaType != "image/png" || im.Width != 3 || im.Height != 2 || im.Upload != "1-shot.png" || len(im.Data) == 0 {
		t.Errorf("image = %+v", im)
	}
}

func TestLoadPaneImagesRefusesPathsOutsideThePanesFolder(t *testing.T) {
	root := t.TempDir()
	data := testPNG(t, 1, 1)
	own := writeUpload(t, root, "pane-a", "1-ok.png", data)
	other := writeUpload(t, root, "pane-b", "1-theirs.png", data)
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.png")
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	nested := writeUpload(t, filepath.Join(root, "pane-a"), "sub", "1-deep.png", data)

	cases := map[string]string{
		"another pane's upload":    other,
		"a file elsewhere":         outside,
		"traversal into a pane":    filepath.Join(root, "pane-a", "..", "pane-b", "1-theirs.png"),
		"traversal out of the dir": filepath.Join(root, "pane-a", "..", "..", filepath.Base(outsideDir), "outside.png"),
		"a subfolder of the pane":  nested,
		"the folder itself":        filepath.Join(root, "pane-a"),
		"a relative path":          filepath.Join("pane-a", "1-ok.png"),
		"an empty path":            "",
		"a nul byte":               own + "\x00.png",
	}
	if runtime.GOOS == "windows" {
		cases["a drive-relative path"] = "C:ok.png"
		cases["a rooted path with no drive"] = `\pane-a\ok.png`
		cases["a UNC path"] = `\\localhost\c$\Windows\win.ini`
		cases["a UNC path to the same file"] = `\\localhost\` + strings.Replace(own, ":", "$", 1)
		cases["a device path"] = `\\.\` + own
	}
	for name, path := range cases {
		got, err := loadPaneImages(root, "pane-a", []imageRef{{Path: path}})
		if err == nil || len(got) != 0 {
			t.Errorf("%s: %q was read (err %v)", name, path, err)
			continue
		}
		for _, leak := range []string{root, "outside", "pane-b"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: the refusal %q names %q", name, err, leak)
			}
		}
	}
	// A control: the same pane's own file still loads.
	if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: own}}); err != nil {
		t.Errorf("the pane's own upload was refused: %v", err)
	}
}

func TestLoadPaneImagesRefusesAPaneIdThatPicksAnotherFolder(t *testing.T) {
	root := t.TempDir()
	p := writeUpload(t, root, "pane-b", "1-theirs.png", testPNG(t, 1, 1))
	for _, id := range []string{"", ".", "..", "pane-a/../pane-b", `pane-a\..\pane-b`, "../" + filepath.Base(root) + "/pane-b"} {
		if _, err := loadPaneImages(root, id, []imageRef{{Path: p}}); err == nil {
			t.Errorf("pane id %q was accepted", id)
		}
	}
}

func TestLoadPaneImagesFollowsNoLinkOutOfTheFolder(t *testing.T) {
	root := t.TempDir()
	data := testPNG(t, 1, 1)
	secret := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(secret, data, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "pane-a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "1-link.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: link}}); err == nil {
		t.Error("a link to a file outside the folder was read")
	}
}

func TestLoadPaneImagesChecksTheBytesAndTheSizeAndTheCount(t *testing.T) {
	root := t.TempDir()
	notImage := writeUpload(t, root, "pane-a", "1-notes.png", []byte("hello, not a picture"))
	empty := writeUpload(t, root, "pane-a", "1-empty.png", nil)
	big := writeUpload(t, root, "pane-a", "1-big.png", append(testPNG(t, 1, 1), make([]byte, structured.MaxImageBytes)...))
	for name, p := range map[string]string{"text named .png": notImage, "an empty file": empty, "an oversize file": big} {
		if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: p}}); err == nil {
			t.Errorf("%s was read", name)
		}
	}
	gone := filepath.Join(root, "pane-a", "swept.png")
	if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: gone}}); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Errorf("a swept picture: %v", err)
	}
	var refs []imageRef
	for i := 0; i <= structured.MaxImages; i++ {
		refs = append(refs, imageRef{Path: gone})
	}
	if _, err := loadPaneImages(root, "pane-a", refs); err == nil || !strings.Contains(err.Error(), "at most 4") {
		t.Errorf("too many pictures: %v", err)
	}
}

// attachedPNG saves a picture for the pane the way attachImage does and returns
// its path.
func (e *structuredEnv) attachedPNG(t *testing.T, w, h int) string {
	t.Helper()
	path, err := saveAttachedImage(e.child, "shot", "image/png", base64.StdEncoding.EncodeToString(testPNG(t, w, h)))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func (e *structuredEnv) waitIdle(t *testing.T) {
	t.Helper()
	e.waitPrompts(t, 1)
	e.waitStatus(t, e.child, session.StatusIdle)
}

func replyText(entries []transcript.Entry) string {
	var b strings.Builder
	for _, en := range entries {
		if en.Kind == transcript.KindReply {
			b.WriteString(en.Markdown)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestPaneSendWithPicturesGivesTheAgentImageBlocks(t *testing.T) {
	e := newStructuredEnvArgs(t, "plain", acptest.ArgShape+"config")
	e.waitIdle(t)
	path := e.attachedPNG(t, 4, 3)
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "paneSend", ID: e.child, Text: "images", Images: []imageRef{{Path: path}}, Req: "i"}, "paneSend", "notice")
	if typeOf(raw) != "paneSend" {
		t.Fatalf("paneSend with a picture: %s", raw)
	}
	var entries []transcript.Entry
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		entries = e.entries(t)
		if strings.Contains(replyText(entries), "images=1 image/png:") {
			break
		}
	}
	if !strings.Contains(replyText(entries), "images=1 image/png:") {
		t.Fatalf("the agent was not sent the image block; replies: %q", replyText(entries))
	}
	var meta []transcript.PromptImage
	for _, en := range entries {
		if en.Kind == transcript.KindPrompt && en.Text == "images" {
			meta = en.Images
		}
	}
	if len(meta) != 1 || meta[0].MediaType != "image/png" || meta[0].Width != 4 || meta[0].Height != 3 || meta[0].Upload != filepath.Base(path) || strings.ContainsAny(meta[0].Upload, `/\`) {
		t.Errorf("prompt image metadata = %+v", meta)
	}
}

func TestPaneSendWithPicturesIsRefusedByAnAgentThatTakesNone(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.waitIdle(t)
	path := e.attachedPNG(t, 2, 2)
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "paneSend", ID: e.child, Text: "images", Images: []imageRef{{Path: path}}, Req: "i"}, "paneSend", "notice")
	if typeOf(raw) != "notice" || !strings.Contains(string(raw), "This agent does not take images") {
		t.Fatalf("answer = %s", raw)
	}
	if got := e.promptsSent(t); len(got) != 1 {
		t.Errorf("a refused prompt was sent: %q", got)
	}
}

func TestPaneSendRefusesAPathOfAnotherPane(t *testing.T) {
	e := newStructuredEnvArgs(t, "plain", acptest.ArgShape+"config")
	e.waitIdle(t)
	other, err := saveAttachedImage("some-other-pane", "shot", "image/png", base64.StdEncoding.EncodeToString(testPNG(t, 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "paneSend", ID: e.child, Text: "images", Images: []imageRef{{Path: other}}, Req: "i"}, "paneSend", "notice")
	if typeOf(raw) != "notice" || !strings.Contains(string(raw), "not attached to this pane") {
		t.Fatalf("answer = %s", raw)
	}
	if got := e.promptsSent(t); len(got) != 1 {
		t.Errorf("a refused prompt was sent: %q", got)
	}
}

func TestLoadPaneImagesWithNoFolderSaysGoneOnlyForItsOwnPath(t *testing.T) {
	root := t.TempDir()
	own := filepath.Join(root, "pane-a", "1-shot.png")
	if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: own}}); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Errorf("a swept pane folder: %v", err)
	}
	theirs := filepath.Join(root, "pane-b", "1-shot.png")
	if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: theirs}}); err == nil || !strings.Contains(err.Error(), "not attached to this pane") {
		t.Errorf("another pane's path with no folder: %v", err)
	}
}

func TestLoadPaneImagesRefusesNamesAFileOfTheUploaderWouldNotHave(t *testing.T) {
	root := t.TempDir()
	own := writeUpload(t, root, "pane-a", "1-ok.png", testPNG(t, 1, 1))
	dir := filepath.Dir(own)
	// A name the uploader never makes is not read, even when it is a picture.
	odd := writeUpload(t, root, "pane-a", "plain.png", testPNG(t, 1, 1))
	cases := map[string]string{
		"an alternate data stream": own + ":stream",
		"a colon in the name":      filepath.Join(dir, "1-a:b.png"),
		"a reserved device name":   filepath.Join(dir, "CON"),
		"a device name with ext":   filepath.Join(dir, "NUL.png"),
		"a name with no number":    odd,
		"a name with a space":      filepath.Join(dir, "1-a b.png"),
	}
	for name, p := range cases {
		if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: p}}); err == nil {
			t.Errorf("%s: %q was read", name, p)
		}
	}
	if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: own}}); err != nil {
		t.Errorf("the control file was refused: %v", err)
	}
}

// A junction needs no privilege on Windows, so it stands in for the symbolic link
// the test above skips there.
func TestLoadPaneImagesFollowsNoJunctionOutOfTheFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows only")
	}
	root := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "1-x.png"), testPNG(t, 1, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "pane-a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "sub"), elsewhere).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
	if _, err := loadPaneImages(root, "pane-a", []imageRef{{Path: filepath.Join(dir, "sub", "1-x.png")}}); err == nil {
		t.Error("a file reached through a junction to outside the folder was read")
	}
}

func TestPaneSendWithPicturesIsRateLimitedPerWindow(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.waitIdle(t)
	bad := []imageRef{{Path: filepath.Join(t.TempDir(), "1-x.png")}}
	for i := 0; i < sendImagesRateLimit; i++ {
		raw := structuredReply(t, e.srv, e.c, command{Cmd: "paneSend", ID: e.child, Text: "x", Images: bad, Req: "r"}, "notice")
		if strings.Contains(string(raw), "too quickly") {
			t.Fatalf("send %d was limited too early: %s", i+1, raw)
		}
	}
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "paneSend", ID: e.child, Text: "x", Images: bad, Req: "r"}, "notice")
	if !strings.Contains(string(raw), "too quickly") {
		t.Errorf("the send past the limit was answered %s", raw)
	}
}
