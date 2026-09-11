// Command statedump prints what a running instance reports about its panes.
// Development aid, not part of the product.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/coder/websocket"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: statedump "ws://127.0.0.1:PORT/ws/control?t=TOKEN"`)
		os.Exit(2)
	}
	conn, _, err := websocket.Dial(context.Background(), os.Args[1], nil)
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
		Panes map[string]struct {
			Kind   string `json:"kind"`
			Agent  string `json:"agent"`
			Model  string `json:"model"`
			Cols   int    `json:"cols"`
			Rows   int    `json:"rows"`
			Status string `json:"status"`
			Err    string `json:"err"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}
	// Ranging over the map directly would order the panes differently on
	// every run, which makes two dumps impossible to compare.
	ids := make([]string, 0, len(st.Panes))
	for id := range st.Panes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := st.Panes[id]
		fmt.Printf("pane %s  %-6s %-20s %dx%d  %-8s err=%q\n",
			short(id), p.Kind, running(p.Agent, p.Model), p.Cols, p.Rows, p.Status, p.Err)
	}
}

// running names what an agent pane is running. A shell pane runs neither and
// gets a dash, so the columns still line up when a dump holds both kinds.
//
// The separator is a plain ASCII one rather than the interpunct the pane
// header uses: this is printed to whatever console the developer happens to
// have, and a Windows one on a legacy codepage would make mojibake of it.
func running(agent, model string) string {
	switch {
	case agent == "":
		return "-"
	case model == "":
		return agent
	}
	return agent + " | " + model
}

// short trims an id to the prefix that is enough to recognise a pane by eye,
// without assuming every id is long enough to slice.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
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
