package credential

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Credential struct {
	HeaderName   string `json:"header_name"`
	HeaderPrefix string `json:"header_prefix"`
	Value        string `json:"value"`
}

type resolveRequest struct {
	AgentID    string `json:"agent_id"`
	TargetHost string `json:"target_host"`
	UserID     string `json:"user_id,omitempty"`
}

type resolveResponse struct {
	Credential *Credential `json:"credential"`
}

type Fetcher struct {
	vaultURI string
	client   *http.Client
}

func NewFetcher(vaultURI string) *Fetcher {
	return &Fetcher{
		vaultURI: strings.TrimRight(vaultURI, "/"),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (f *Fetcher) Fetch(agentID, targetHost, userID string) (*Credential, error) {
	if f.vaultURI == "" {
		return nil, nil // Vault not configured, do nothing
	}

	reqBody, err := json.Marshal(resolveRequest{
		AgentID:    agentID,
		TargetHost: targetHost,
		UserID:     userID,
	})
	if err != nil {
		return nil, err
	}

	resp, err := f.client.Post(fmt.Sprintf("%s/internal/credentials/resolve", f.vaultURI), "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // No credential found
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vault resolve returned %d: %s", resp.StatusCode, string(body))
	}

	var payload resolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	return payload.Credential, nil
}
