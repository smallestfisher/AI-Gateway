package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/registry"
)

// UpstreamModel is a model advertised by an upstream provider's /v1/models API.
type UpstreamModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

type upstreamProvider struct {
	ID       string
	Name     string
	Protocol string
	BaseURL  string
	APIKey   string
}

// ListUpstreamModels asks the provider for its supported models. Most OpenAI-
// compatible and Anthropic-compatible providers expose GET /v1/models.
func (s *Store) ListUpstreamModels(ctx context.Context, providerID string) ([]UpstreamModel, error) {
	if providerID == "" {
		return nil, ErrValidation
	}
	p, err := s.getUpstreamProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	url := strings.TrimRight(p.BaseURL, "/") + "/v1/models"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range modelListAuthHeaders(p.Protocol, p.APIKey) {
		req.Header.Set(k, v)
	}
	for k, v := range egress.DefaultClientHeaders(adapter.Protocol(p.Protocol)) {
		req.Header.Set(k, v)
	}
	if profile, err := s.getDiagnosticProviderProfile(ctx, providerID); err == nil {
		applyClientProfileHeader(req.Header, profile)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("admin: fetch upstream models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("admin: upstream models returned status %d", resp.StatusCode)
	}

	var body struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("admin: decode upstream models: %w", err)
	}
	out := make([]UpstreamModel, 0, len(body.Data))
	seen := make(map[string]struct{}, len(body.Data))
	for _, m := range body.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, UpstreamModel{ID: id, DisplayName: strings.TrimSpace(m.DisplayName)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) getUpstreamProvider(ctx context.Context, id string) (upstreamProvider, error) {
	var p upstreamProvider
	var key []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, protocol, base_url, api_key_enc FROM providers WHERE id=$1`, id,
	).Scan(&p.ID, &p.Name, &p.Protocol, &p.BaseURL, &key)
	if err != nil {
		return p, err
	}
	p.APIKey = string(key)
	return p, nil
}

func (s *Store) getDiagnosticProvider(ctx context.Context, id string) (*registry.Provider, error) {
	if id == "" {
		return nil, ErrValidation
	}
	var (
		p             registry.Provider
		proto         string
		apiKey        []byte
		proxyURL      *string
		timeoutMS     int
		connTimeoutMS int
		maxRetries    int
		metadata      []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.name, p.protocol, p.base_url, p.api_key_enc,
		       pe.url, p.timeout_ms, p.connect_timeout_ms, p.max_retries, p.metadata
		FROM providers p
		LEFT JOIN proxy_egress pe ON pe.id = p.proxy_id AND pe.enabled = true
		WHERE p.id = $1`, id).Scan(
		&p.ID, &p.Name, &proto, &p.BaseURL, &apiKey, &proxyURL,
		&timeoutMS, &connTimeoutMS, &maxRetries, &metadata,
	)
	if err != nil {
		return nil, err
	}
	p.Protocol = adapter.Protocol(proto)
	p.APIKey = string(apiKey)
	p.Timeout = time.Duration(timeoutMS) * time.Millisecond
	p.ConnectTimeout = time.Duration(connTimeoutMS) * time.Millisecond
	p.MaxRetries = maxRetries
	if proxyURL != nil {
		p.ProxyURL = *proxyURL
	}
	p.Headers = extractProviderHeaders(metadata)
	return &p, nil
}

func (s *Store) getDiagnosticProviderProfile(ctx context.Context, providerID string) (*registry.ClientProfile, error) {
	if providerID == "" {
		return nil, ErrValidation
	}
	rows, err := s.pool.Query(ctx, `
		SELECT scope, headers, COALESCE(user_agent,''), COALESCE(origin,''),
		       COALESCE(referer,''), strip_client_headers
		FROM client_profiles
		WHERE enabled=true
		  AND (scope='default' OR (scope='provider' AND target_id=$1))
		ORDER BY CASE WHEN scope='default' THEN 0 ELSE 1 END`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &registry.ClientProfile{Headers: map[string]string{}}
	applied := false
	for rows.Next() {
		var (
			scope, ua, origin, referer string
			headers                    []byte
			strip                      bool
		)
		if err := rows.Scan(&scope, &headers, &ua, &origin, &referer, &strip); err != nil {
			return nil, err
		}
		cp := &registry.ClientProfile{UserAgent: ua, Origin: origin, Referer: referer, StripClientHeaders: strip}
		if len(headers) > 0 {
			_ = json.Unmarshal(headers, &cp.Headers)
		}
		mergeDiagnosticProfile(out, cp)
		applied = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !applied || emptyDiagnosticProfile(out) {
		return nil, nil
	}
	return out, nil
}

func mergeDiagnosticProfile(dst, src *registry.ClientProfile) {
	if src == nil {
		return
	}
	if dst.Headers == nil {
		dst.Headers = map[string]string{}
	}
	for k, v := range src.Headers {
		dst.Headers[k] = v
	}
	if src.UserAgent != "" {
		dst.UserAgent = src.UserAgent
	}
	if src.Origin != "" {
		dst.Origin = src.Origin
	}
	if src.Referer != "" {
		dst.Referer = src.Referer
	}
	if src.StripClientHeaders {
		dst.StripClientHeaders = true
	}
}

func emptyDiagnosticProfile(p *registry.ClientProfile) bool {
	return p == nil || (len(p.Headers) == 0 && p.UserAgent == "" && p.Origin == "" && p.Referer == "" && !p.StripClientHeaders)
}

func applyClientProfileHeader(h http.Header, p *registry.ClientProfile) {
	if p == nil {
		return
	}
	for k, v := range p.Headers {
		h.Set(k, v)
	}
	if p.UserAgent != "" {
		h.Set("User-Agent", p.UserAgent)
	}
	if p.Origin != "" {
		h.Set("Origin", p.Origin)
	}
	if p.Referer != "" {
		h.Set("Referer", p.Referer)
	}
}

func extractProviderHeaders(metadata []byte) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	var doc struct {
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(metadata, &doc) != nil {
		return nil
	}
	return doc.Headers
}

func modelListAuthHeaders(protocol, key string) map[string]string {
	switch protocol {
	case "anthropic_messages":
		return map[string]string{
			"x-api-key":         key,
			"anthropic-version": "2023-06-01",
		}
	default:
		return map[string]string{"Authorization": "Bearer " + key}
	}
}
