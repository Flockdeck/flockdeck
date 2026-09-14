package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The favicon is the application's own icon, copied rather than drawn again
// (see cmd/sitegen's TestFaviconIsTheAppIcon, which this mirrors), so the
// two must stay the same picture. Line endings are set aside: a Windows
// checkout holds either file with CRLF.
func TestFaviconIsTheAppIcon(t *testing.T) {
	icon, err := assets.ReadFile("assets/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(lf(icon)) != string(lf(app)) {
		t.Error("cmd/docgen/assets/favicon.svg differs from internal/webui/assets/icon.svg; copy the app's icon over it")
	}

	iconICO, err := assets.ReadFile("assets/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	ico, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(iconICO, ico) {
		t.Error("cmd/docgen/assets/favicon.ico differs from internal/webui/assets/icon.ico; copy the app's icon over it")
	}

	touchIcon, err := assets.ReadFile("assets/apple-touch-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	touch, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon-180.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(touchIcon, touch) {
		t.Error("cmd/docgen/assets/apple-touch-icon.png differs from internal/webui/assets/icon-180.png; copy the app's icon over it")
	}
}
