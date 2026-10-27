package codexacp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	imgDirPrefix = "flockdeck-codex-img-"
	// maxImage is the most bytes of one decoded image, and maxImages how many a
	// prompt may carry.
	maxImage  = 20 << 20
	maxImages = 16
	maxBlocks = 256
)

// sweepImageDirs removes the image directories of sessions whose adapter was
// killed before it could clean up. Only those a week old are touched, so
// another adapter's live session is left alone.
func sweepImageDirs() {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), imgDirPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			_ = os.RemoveAll(filepath.Join(tmp, e.Name()))
		}
	}
}

var imageExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// stageImage writes an image to a file codex can read, in a directory private
// to the session and removed when it ends, and returns the path.
func (ss *session) stageImage(mime, data string) (string, error) {
	ext, ok := imageExt[strings.ToLower(mime)]
	if !ok {
		return "", errors.New("unsupported image type")
	}
	if base64.StdEncoding.DecodedLen(len(data)) > maxImage+3 {
		return "", errors.New("the image is too large")
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", errors.New("the image is not valid base64")
	}
	if len(raw) > maxImage {
		return "", errors.New("the image is too large")
	}
	ss.imgMu.Lock()
	defer ss.imgMu.Unlock()
	if closedCh(ss.quit) {
		return "", errors.New("the session is closed")
	}
	if ss.imgDir == "" {
		dir, err := os.MkdirTemp("", imgDirPrefix+"*") // mode 0700
		if err != nil {
			return "", errors.New("could not store the image")
		}
		ss.imgDir = dir
	}
	path := filepath.Join(ss.imgDir, uuid.NewString()+ext)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", errors.New("could not store the image")
	}
	_, werr := f.Write(raw)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return "", errors.New("could not store the image")
	}
	return path, nil
}

func textInput(s string) map[string]any {
	return map[string]any{"type": "text", "text": s, "text_elements": []any{}}
}

// promptRequest turns the blocks of a session/prompt into a turn. Text and
// images map one to one; an image goes through a file. An embedded text
// resource and a resource link become text that names the resource. Audio is
// refused. A prompt that is only /review or /compact, or that starts with the
// name of a skill, becomes the matching codex call; any other text that starts
// with a slash is sent as it is.
func (ss *session) promptRequest(raw json.RawMessage) (*promptReq, error) {
	var in []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
		URI      string `json:"uri"`
		Name     string `json:"name"`
		Resource *struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errors.New("prompt must be an array of content blocks")
	}
	if len(in) == 0 || len(in) > maxBlocks {
		return nil, errors.New("the prompt is empty or has too many blocks")
	}
	req := &promptReq{kind: "turn"}
	skip := 0
	if in[0].Type == "text" && strings.HasPrefix(strings.TrimSpace(in[0].Text), "/") {
		name, args, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(in[0].Text), "/"), " ")
		args = strings.TrimSpace(args)
		ss.mu.Lock()
		path, isSkill := ss.skills[name]
		ss.mu.Unlock()
		switch {
		case strings.EqualFold(name, "compact") && len(in) == 1:
			req.kind = "compact"
			return req, nil
		case strings.EqualFold(name, "review") && len(in) == 1:
			req.kind, req.text = "review", args
			return req, nil
		case isSkill:
			req.input = append(req.input, map[string]any{"type": "skill", "name": name, "path": path})
			if args != "" {
				req.input = append(req.input, textInput(args))
			}
			skip = 1
		}
	}
	images := 0
	for _, b := range in[skip:] {
		switch b.Type {
		case "text":
			req.input = append(req.input, textInput(b.Text))
		case "image":
			if b.Data == "" || b.MimeType == "" {
				return nil, errors.New("an image block needs data and mimeType")
			}
			if images++; images > maxImages {
				return nil, errors.New("too many images")
			}
			path, err := ss.stageImage(b.MimeType, b.Data)
			if err != nil {
				return nil, err
			}
			req.input = append(req.input, map[string]any{"type": "localImage", "path": path})
		case "resource":
			if b.Resource == nil || b.Resource.Text == "" {
				return nil, errors.New("only text resources can be sent")
			}
			req.input = append(req.input, textInput(fmt.Sprintf("<resource uri=%q>\n%s\n</resource>", b.Resource.URI, b.Resource.Text)))
		case "resource_link":
			name := b.Name
			if name == "" {
				name = b.URI
			}
			req.input = append(req.input, textInput(fmt.Sprintf("[%s](%s)", name, b.URI)))
		default:
			return nil, errors.New("unsupported content block type")
		}
	}
	if len(req.input) == 0 {
		return nil, errors.New("the prompt is empty")
	}
	return req, nil
}

var mcpNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// mcpConfig turns the MCP servers of a session/new into codex's mcp_servers
// configuration, which goes to codex in the thread/start request. Nothing is
// written to disk or to the command line, where a token would show. It
// returns nil when there are none.
func mcpConfig(servers []json.RawMessage) (map[string]any, error) {
	if len(servers) == 0 {
		return nil, nil
	}
	if len(servers) > 32 {
		return nil, errors.New("too many mcpServers")
	}
	cfg := map[string]any{}
	for _, raw := range servers {
		var s struct {
			Type    string   `json:"type"`
			Name    string   `json:"name"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Env     []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"env"`
			URL     string `json:"url"`
			Headers []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"headers"`
		}
		if err := json.Unmarshal(raw, &s); err != nil || !mcpNameRe.MatchString(s.Name) {
			return nil, errors.New("an mcpServers entry needs a plain name")
		}
		if _, dup := cfg[s.Name]; dup {
			return nil, errors.New("two mcpServers have the name " + s.Name)
		}
		switch strings.ToLower(s.Type) {
		case "sse":
			return nil, errors.New("SSE mcpServers are not supported")
		case "http":
			if s.URL == "" {
				return nil, errors.New("mcp server " + s.Name + " has no url")
			}
			h := map[string]string{}
			for _, kv := range s.Headers {
				h[kv.Name] = kv.Value
			}
			c := map[string]any{"url": s.URL}
			if len(h) > 0 {
				c["http_headers"] = h
			}
			cfg[s.Name] = c
		default:
			if s.Command == "" {
				return nil, errors.New("mcp server " + s.Name + " has no command")
			}
			args := s.Args
			if args == nil {
				args = []string{}
			}
			c := map[string]any{"command": s.Command, "args": args}
			if len(s.Env) > 0 {
				env := map[string]string{}
				for _, kv := range s.Env {
					env[kv.Name] = kv.Value
				}
				c["env"] = env
			}
			cfg[s.Name] = c
		}
	}
	return cfg, nil
}
