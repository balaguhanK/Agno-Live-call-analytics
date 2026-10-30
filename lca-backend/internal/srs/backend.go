package srs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Backend posts recorded calls to the dashboard backend's ingest API.
type Backend struct {
	BaseURL string // e.g. http://localhost:8090
	Token   string // INGEST_TOKEN
	HTTP    *http.Client
}

func (b *Backend) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.BaseURL+"/api/ingest"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("backend unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&e)
		return fmt.Errorf("%s %s: %s %s", method, path, res.Status, e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Agents lists active agent IDs.
func (b *Backend) Agents(ctx context.Context) ([]string, error) {
	var out struct {
		Agents []string `json:"agents"`
	}
	err := b.do(ctx, http.MethodGet, "/agents", nil, &out)
	return out.Agents, err
}

// CreateCall starts a call and returns its ID.
func (b *Backend) CreateCall(ctx context.Context, agent, callerNumber string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := b.do(ctx, http.MethodPost, "/calls", map[string]string{
		"agent": agent, "callerPhoneNumber": callerNumber, "systemPhoneNumber": "SIPREC",
	}, &out)
	return out.ID, err
}

// AddLine saves one transcribed line; the backend redacts and analyzes it.
func (b *Backend) AddLine(ctx context.Context, callID, speaker, text string, startSec, endSec float64) error {
	return b.do(ctx, http.MethodPost, "/calls/"+url.PathEscape(callID)+"/lines", map[string]any{
		"speaker": speaker, "text": text, "startSec": startSec, "endSec": endSec,
	}, nil)
}

// EndCall marks the call Done; the backend writes categories and a summary.
func (b *Backend) EndCall(ctx context.Context, callID string, durationSec float64) error {
	return b.do(ctx, http.MethodPost, "/calls/"+url.PathEscape(callID)+"/end", map[string]any{"durationSec": durationSec}, nil)
}
