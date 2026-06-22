package admin

import (
	"context"
	"fmt"

	"github.com/aigateway/ai-hub/internal/health"
)

// HealthStatsProvider is implemented by the Redis-backed health store.
type HealthStatsProvider interface {
	Stats(ctx context.Context, providerID, model string) (health.Stats, error)
}

type HealthStatus struct {
	Data    []HealthRow `json:"data"`
	Warning string      `json:"warning,omitempty"`
}

type HealthRow struct {
	ProviderID    string           `json:"provider_id"`
	ProviderName  string           `json:"provider_name"`
	UpstreamModel string           `json:"upstream_model"`
	Total         int              `json:"total"`
	Failures      int              `json:"failures"`
	Slow          int              `json:"slow"`
	ErrorRate     float64          `json:"error_rate"`
	SlowRate      float64          `json:"slow_rate"`
	Open          bool             `json:"open"`
	OpenedAgoS    float64          `json:"opened_ago_s"`
	Thresholds    HealthThresholds `json:"thresholds"`
}

type HealthThresholds struct {
	ErrorRate   float64 `json:"error_rate"`
	P95TTFTMs   int     `json:"p95_ttft_ms"`
	WindowSec   int     `json:"window_sec"`
	CooldownSec int     `json:"cooldown_sec"`
}

func (s *Store) ListHealth(ctx context.Context, hp HealthStatsProvider) (HealthStatus, error) {
	if s == nil || s.pool == nil {
		return HealthStatus{}, ErrValidation
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT p.id, p.name, mc.upstream_model,
		       p.hc_error_rate, p.hc_p95_ttft_ms, p.hc_window_sec, p.hc_cooldown_sec
		FROM model_channels mc
		JOIN providers p ON p.id = mc.provider_id
		JOIN models m ON m.id = mc.model_id
		WHERE mc.enabled = true AND p.enabled = true AND m.enabled = true
		ORDER BY p.name, mc.upstream_model`)
	if err != nil {
		return HealthStatus{}, err
	}
	defer rows.Close()

	out := HealthStatus{Data: []HealthRow{}}
	if hp == nil {
		out.Warning = "health stats unavailable"
	}
	for rows.Next() {
		var row HealthRow
		if err := rows.Scan(
			&row.ProviderID,
			&row.ProviderName,
			&row.UpstreamModel,
			&row.Thresholds.ErrorRate,
			&row.Thresholds.P95TTFTMs,
			&row.Thresholds.WindowSec,
			&row.Thresholds.CooldownSec,
		); err != nil {
			return HealthStatus{}, err
		}
		if hp != nil {
			stats, err := hp.Stats(ctx, row.ProviderID, row.UpstreamModel)
			if err != nil {
				out.Warning = fmt.Sprintf("health stats unavailable: %v", err)
			} else {
				row.Total = stats.Total
				row.Failures = stats.Failures
				row.Slow = stats.Slow
				row.ErrorRate = stats.ErrorRate
				row.SlowRate = stats.SlowRate
				row.Open = stats.Open
				row.OpenedAgoS = stats.OpenedAgoS
			}
		}
		out.Data = append(out.Data, row)
	}
	return out, rows.Err()
}
