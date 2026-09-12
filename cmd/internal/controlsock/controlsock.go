// Package controlsock is what the development tools under cmd share for
// talking to a running instance's control socket.
package controlsock

import (
	"context"
	"encoding/json"

	"github.com/coder/websocket"
)

// ReadState returns the first state snapshot on the socket. A window is
// greeted with the key table and preferences before the state, so the first
// message is not the one wanted, and the snapshot of a busy workspace is
// larger than the socket's default 32 KB limit on a message.
func ReadState(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
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
