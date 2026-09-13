package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
)

// modelLister is a wire that can ask its endpoint which models it offers.
//
// It is how /model has something to offer for an agent whose catalog entry
// lists no models -- a local Ollama or LM Studio above all -- where the model
// has to be named by its exact tag, which nobody remembers.
type modelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

func (w *openaiWire) ListModels(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, openaiEndpoint(w.base, "/models"), w.header(), &out); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	slices.Sort(ids)
	return ids, nil
}

func (w *anthropicWire) ListModels(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, endpoint(w.base, "https://api.anthropic.com", "v1", "/models?limit=100"), w.header(), &out); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	// Newest first, the way the API lists them, which is the order worth
	// choosing from.
	return ids, nil
}

func (w *geminiWire) ListModels(ctx context.Context) ([]string, error) {
	var out struct {
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := getJSON(ctx, endpoint(w.base, "https://generativelanguage.googleapis.com", "v1beta", "/models?pageSize=200"), w.header(), &out); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range out.Models {
		// Embedding models and the like are listed too, and cannot hold a
		// conversation.
		if slices.Contains(m.Methods, "generateContent") {
			ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// getJSON fetches one JSON document, turning a failure into the same error a
// failed request for an answer is.
func getJSON(ctx context.Context, url string, header http.Header, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header = header.Clone()
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return &apiError{Code: resp.StatusCode, Status: resp.Status, Msg: apiMessage(msg)}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}
