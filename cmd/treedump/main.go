// Command treedump prints the tab and split structure a running instance is
// showing, so a rearrangement sent with ctl can be read back without looking
// at the window. Development aid, not part of the product.
//
//	go run ./cmd/treedump [-full] "ws://127.0.0.1:PORT/ws/control?t=TOKEN"
//
// Ids are shortened to their first 8 characters, which is enough to follow a
// layout by eye; -full prints them in the form ctl wants them back.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/coder/websocket"
)

// node mirrors the layout tree as the control socket reports it.
type node struct {
	Pane     string  `json:"pane"`
	Dir      string  `json:"dir"`
	Children []*node `json:"children"`
}

// full prints whole uuids rather than a readable prefix.
var full bool

func main() {
	// -full is accepted on either side of the URL: writing the flag first is
	// the habit every other command teaches, and taking it as the URL only
	// produced a dial error that named the flag.
	url := ""
	for _, arg := range os.Args[1:] {
		switch {
		case arg == "-full" || arg == "--full":
			full = true
		case strings.HasPrefix(arg, "-"):
			usage() // -h, most likely, which would otherwise be dialled
		case url == "":
			url = arg
		default:
			fmt.Fprintf(os.Stderr, "treedump: unexpected argument %q\n", arg)
			usage()
		}
	}
	if url == "" {
		usage()
	}

	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer conn.CloseNow()

	data, err := readState(context.Background(), conn)
	if err != nil {
		fmt.Println("read:", err)
		os.Exit(1)
	}
	var st struct {
		ActiveTab string `json:"activeTab"`
		Tabs      []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Focus string `json:"focus"`
			Root  *node  `json:"root"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}

	for _, t := range st.Tabs {
		mark := " "
		if t.ID == st.ActiveTab {
			mark = "*"
		}
		fmt.Printf("%s tab %-14s id=%s focus=%s  %s\n",
			mark, t.Title, short(t.ID), short(t.Focus), render(t.Root))
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: treedump [-full] "ws://127.0.0.1:PORT/ws/control?t=TOKEN"`)
	os.Exit(2)
}

func short(s string) string {
	if full || len(s) <= 8 {
		return s
	}
	return s[:8]
}

// render draws a subtree as h[a b] / v[a b], which reads the way the panes are
// arranged on screen.
func render(n *node) string {
	if n == nil {
		return "-"
	}
	if n.Pane != "" {
		return short(n.Pane)
	}
	parts := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		parts = append(parts, render(c))
	}
	return n.Dir + "[" + strings.Join(parts, " ") + "]"
}

// readState returns the first state snapshot on the socket. A window is greeted
// with the key table and preferences before the state, so the first message is
// not the one wanted, and the snapshot of a busy workspace is larger than the
// socket's default 32 KB limit on a message.
func readState(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	conn.SetReadLimit(16 << 20)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var msg struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "state" {
			return data, nil
		}
	}
}
