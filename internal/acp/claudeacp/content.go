package claudeacp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const mcpDirPrefix = "flockdeck-mcp-"

// sweepMCPDirs removes the config directories of sessions whose adapter was
// killed before it could clean up. Only those a week old are touched, so another
// adapter's live session is left alone.
func sweepMCPDirs() {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), mcpDirPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			_ = os.RemoveAll(filepath.Join(tmp, e.Name()))
		}
	}
}

// promptContent turns the blocks of a session/prompt into the content blocks of
// a stream-json user message. Text and images map one to one. An embedded text
// resource and a resource link become text that names the resource, as the
// agent has no other way to take them. Audio is refused.
func promptContent(raw json.RawMessage) ([]map[string]any, error) {
	var in []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
		URI      string `json:"uri"`
		Name     string `json:"name"`
		Resource *struct {
			URI      string `json:"uri"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Blob     string `json:"blob"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("prompt must be an array of content blocks: %w", err)
	}
	var out []map[string]any
	for _, b := range in {
		switch b.Type {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case "image":
			if b.Data == "" || b.MimeType == "" {
				return nil, errors.New("an image block needs data and mimeType")
			}
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": b.MimeType, "data": b.Data}})
		case "resource":
			if b.Resource == nil || b.Resource.Text == "" {
				return nil, errors.New("only text resources can be sent")
			}
			out = append(out, map[string]any{"type": "text", "text": fmt.Sprintf("<resource uri=%q>\n%s\n</resource>", b.Resource.URI, b.Resource.Text)})
		case "resource_link":
			name := b.Name
			if name == "" {
				name = b.URI
			}
			out = append(out, map[string]any{"type": "text", "text": fmt.Sprintf("[%s](%s)", name, b.URI)})
		default:
			return nil, errors.New("unsupported content block type " + b.Type)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the prompt is empty")
	}
	return out, nil
}

// writeMCPConfig writes the MCP servers of a session/new to a file for claude's
// --mcp-config, and returns its path, or "" when there are none. The file is
// private to the user and removed when the session ends.
func writeMCPConfig(servers []json.RawMessage) (string, error) {
	if len(servers) == 0 {
		return "", nil
	}
	cfg := map[string]any{}
	for _, raw := range servers {
		var s struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Command string `json:"command"`
			Args    []string
			Env     []struct{ Name, Value string }
			URL     string `json:"url"`
			Headers []struct{ Name, Value string }
		}
		if err := json.Unmarshal(raw, &s); err != nil || s.Name == "" {
			return "", errors.New("an mcpServers entry needs a name")
		}
		switch strings.ToLower(s.Type) {
		case "http", "sse":
			h := map[string]string{}
			for _, kv := range s.Headers {
				h[kv.Name] = kv.Value
			}
			cfg[s.Name] = map[string]any{"type": strings.ToLower(s.Type), "url": s.URL, "headers": h}
		default:
			if s.Command == "" {
				return "", errors.New("mcp server " + s.Name + " has no command")
			}
			env := map[string]string{}
			for _, kv := range s.Env {
				env[kv.Name] = kv.Value
			}
			args := s.Args
			if args == nil {
				args = []string{}
			}
			cfg[s.Name] = map[string]any{"type": "stdio", "command": s.Command, "args": args, "env": env}
		}
	}
	b, err := json.Marshal(map[string]any{"mcpServers": cfg})
	if err != nil {
		return "", err
	}
	// The file holds tokens (env values, headers): a directory of its own,
	// private to the user, that is removed whole with the session.
	dir, err := os.MkdirTemp("", mcpDirPrefix+"*")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}
