package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// LocalEmbedProvider — calls a local sidecar /embed endpoint.
// ---------------------------------------------------------------------------

// LocalEmbedProvider implements EmbedProvider by calling a local HTTP sidecar.
type LocalEmbedProvider struct {
	baseURL string
	model   string
	dims    int
	client  *http.Client
}

// NewLocalEmbedProvider creates a LocalEmbedProvider that posts to baseURL/embed.
// dims must match the dimensionality produced by the sidecar model.
func NewLocalEmbedProvider(baseURL, model string, dims int) *LocalEmbedProvider {
	return &LocalEmbedProvider{
		baseURL: baseURL,
		model:   model,
		dims:    dims,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed sends texts to the sidecar and returns one embedding per text.
func (p *LocalEmbedProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := struct {
		Input []string `json:"input"`
		Model string   `json:"model"`
	}{Input: texts, Model: p.model}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/embed", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: sidecar returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("embed: unmarshal response: %w", err)
	}

	// Return embeddings in original input order (data may arrive out of order).
	vecs := make([][]float32, len(texts))
	for _, item := range result.Data {
		if item.Index < 0 || item.Index >= len(vecs) {
			return nil, fmt.Errorf("embed: response index %d out of range", item.Index)
		}
		vecs[item.Index] = item.Embedding
	}
	return vecs, nil
}

// Dims returns the configured embedding dimensionality.
func (p *LocalEmbedProvider) Dims() int { return p.dims }

// Probe checks the local sidecar's capability endpoint without submitting
// embedding content. It is intentionally suitable for bounded health checks.
func (p *LocalEmbedProvider) Probe(ctx context.Context) error {
	return probeLocalSidecar(ctx, p.client, p.baseURL)
}

// ---------------------------------------------------------------------------
// LocalRerankProvider — calls a local sidecar /rerank endpoint.
// ---------------------------------------------------------------------------

// LocalRerankProvider implements RerankProvider by calling a local HTTP sidecar.
type LocalRerankProvider struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewLocalRerankProvider creates a LocalRerankProvider that posts to baseURL/rerank.
func NewLocalRerankProvider(baseURL, model string) *LocalRerankProvider {
	return &LocalRerankProvider{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Rerank sends the query and candidate documents to the sidecar and returns
// up to topN results sorted by descending relevance score.
func (p *LocalRerankProvider) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	reqBody := struct {
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
		Model     string   `json:"model"`
		TopN      int      `json:"top_n"`
	}{Query: query, Documents: documents, Model: p.model, TopN: topN}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("rerank: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/rerank", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("rerank: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rerank: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank: sidecar returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Results []RerankResult `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("rerank: unmarshal response: %w", err)
	}

	return result.Results, nil
}

// Probe checks the local sidecar's capability endpoint without submitting a
// query or documents.
func (p *LocalRerankProvider) Probe(ctx context.Context) error {
	return probeLocalSidecar(ctx, p.client, p.baseURL)
}

func probeLocalSidecar(ctx context.Context, client *http.Client, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("provider probe request: %w", err)
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("provider probe: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("provider probe status %d", response.StatusCode)
	}
	return nil
}
