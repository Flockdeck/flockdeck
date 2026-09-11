// Command ctl drives a running flockdeck instance over its control socket.
// Development aid, not part of the product.
//
//	go run ./cmd/ctl "ws://127.0.0.1:PORT/ws/control?t=TOKEN" '{"type":"split","dir":"h"}' ...
//
// Each argument after the URL is one JSON command. Commands that name a pane
// but leave "id" out are addressed to the focused pane.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: ctl "ws://127.0.0.1:PORT/ws/control?t=TOKEN" [command-json ...]`)
		os.Exit(2)
	}
	url := os.Args[1]
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer conn.CloseNow()

	data, err := readState(ctx, conn)
	if err != nil {
		fmt.Println("read:", err)
		os.Exit(1)
	}
	// activeTab is the id of the active tab, not its position, so the focused
	// pane is found by matching ids rather than by indexing.
	var st struct {
		ActiveTab string `json:"activeTab"`
		Tabs      []struct {
			ID    string `json:"id"`
			Focus string `json:"focus"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		fmt.Println("decode state:", err)
		os.Exit(1)
	}
	focus := ""
	for _, t := range st.Tabs {
		if t.ID == st.ActiveTab {
			focus = t.Focus
			break
		}
	}
	fmt.Println("focused pane:", focus)

	for _, c := range os.Args[2:] {
		var cmd map[string]any
		if err := json.Unmarshal([]byte(c), &cmd); err != nil {
			fmt.Println("bad command:", c, err)
			continue
		}
		if _, ok := cmd["id"]; !ok {
			cmd["id"] = focus
		}
		out, _ := json.Marshal(cmd)
		if err := conn.Write(ctx, websocket.MessageText, out); err != nil {
			fmt.Println("write:", err)
			return
		}
		fmt.Println("sent:", string(out))
		time.Sleep(1500 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
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
