// Command statedump prints what a running instance reports about its panes.
// Development aid, not part of the product.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
	// The first message on the socket is the state snapshot.
	_, data, err := conn.Read(context.Background())
	if err != nil {
		fmt.Println("read:", err)
		os.Exit(1)
	}
	var st struct {
		Panes map[string]struct {
			Kind   string `json:"kind"`
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
	for id, p := range st.Panes {
		fmt.Printf("pane %s  %-6s %dx%d  %-8s err=%q\n", short(id), p.Kind, p.Cols, p.Rows, p.Status, p.Err)
	}
}

// short trims an id to the prefix that is enough to recognise a pane by eye,
// without assuming every id is long enough to slice.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
