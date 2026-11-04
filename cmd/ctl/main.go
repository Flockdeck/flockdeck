// Command ctl drives a running flockdeck instance over its control socket.
// Development aid, not part of the product.
//
//	go run ./cmd/ctl "ws://127.0.0.1:PORT/ws/control?t=TOKEN" '{"cmd":"splitPane","dir":"h"}' ...
//
// Each argument after the URL is one JSON command, named by its "cmd" field as
// the window sends it. Commands that name a pane but leave "id" out are
// addressed to the focused pane.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/cmd/internal/controlsock"
)

func main() {
	// A first argument with a dash is a flag somebody hoped for, -h most
	// likely; dialled as the address it only produced an error naming it.
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
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

	data, err := controlsock.ReadState(ctx, conn)
	if err != nil {
		fmt.Println("read:", err)
		os.Exit(1)
	}
	focus, err := focusedPane(data)
	if err != nil {
		fmt.Println("decode state:", err)
		os.Exit(1)
	}
	var focusMu sync.Mutex
	fmt.Println("focused pane:", focus)

	// A command the server refuses is answered with a notice and nothing else,
	// so notices are printed as they arrive; otherwise a command that did
	// nothing looks exactly like one that worked. A new snapshot is the
	// server saying a command has taken effect, which is when the next can go.
	states := make(chan struct{}, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Error bool   `json:"error"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch {
			case msg.Type == "state":
				// The focus moves as commands run (a split focuses the new
				// pane), so a command with no id goes to where it is now.
				if f, err := focusedPane(data); err == nil && f != "" {
					focusMu.Lock()
					focus = f
					focusMu.Unlock()
				}
				select {
				case states <- struct{}{}:
				default:
				}
			case msg.Type != "notice":
			case msg.Error:
				fmt.Println("refused:", msg.Text)
			default:
				fmt.Println("notice:", msg.Text)
			}
		}
	}()

	for _, c := range os.Args[2:] {
		var cmd map[string]any
		if err := json.Unmarshal([]byte(c), &cmd); err != nil {
			fmt.Println("bad command:", c, err)
			continue
		}
		if _, ok := cmd["id"]; !ok {
			focusMu.Lock()
			cmd["id"] = focus
			focusMu.Unlock()
		}
		out, _ := json.Marshal(cmd)
		select { // a snapshot from before this command says nothing about it
		case <-states:
		default:
		}
		if err := conn.Write(ctx, websocket.MessageText, out); err != nil {
			fmt.Println("write:", err)
			return
		}
		fmt.Println("sent:", string(out))
		// Waiting a fixed second and a half after each one made a script of
		// ten commands take a quarter of a minute. The wait now ends with the
		// snapshot the command causes, and only a command that changes
		// nothing, so causes none, still waits the whole of it.
		select {
		case <-states:
		case <-time.After(1500 * time.Millisecond):
		}
	}
	// Long enough for a notice that follows the last snapshot to be printed.
	time.Sleep(250 * time.Millisecond)
}

// focusedPane is the focused pane of the active tab in a state snapshot.
// activeTab is the id of the active tab, not its position, so it is found by
// matching ids rather than by indexing.
func focusedPane(data []byte) (string, error) {
	var st struct {
		ActiveTab string `json:"activeTab"`
		Tabs      []struct {
			ID    string `json:"id"`
			Focus string `json:"focus"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return "", err
	}
	for _, t := range st.Tabs {
		if t.ID == st.ActiveTab {
			return t.Focus, nil
		}
	}
	return "", nil
}
