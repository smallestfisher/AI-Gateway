package admin

import (
	"context"
	"time"
)

// DashboardStats holds aggregated metrics for the dashboard.
type DashboardStats struct {
	Period string `json:"period"` // e.g., "24h", "7d"

	// Top-level metrics
	TotalRequests int64   `json:"total_requests"`
	ActiveUsers   int     `json:"active_users"`
	ProviderCount int     `json:"provider_count"`
	ModelCount    int     `json:"model_count"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	SuccessRate   float64 `json:"success_rate"`

	// Top models
	TopModels []ModelStats `json:"top_models"`

	// Recent errors
	RecentErrors []ErrorStat `json:"recent_errors"`
}

type ModelStats struct {
	Model       string  `json:"model"`
	Requests    int64   `json:"requests"`
	Percentage  float64 `json:"percentage"`
	AvgLatency  float64 `json:"avg_latency_ms"`
	SuccessRate float64 `json:"success_rate"`
}

type ErrorStat struct {
	Timestamp  string `json:"timestamp"`
	Model      string `json:"model"`
	ProviderID string `json:"provider_id,omitempty"`
	ErrorCode  string `json:"error_code"`
	ErrorMsg   string `json:"error_msg"`
	Status     string `json:"status"`
}

// GetDashboardStats returns dashboard metrics for the last 24 hours.
func (s *Store) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	stats := &DashboardStats{
		Period:       "24h",
		TopModels:    []ModelStats{},
		RecentErrors: []ErrorStat{},
	}
	since := time.Now().Add(-24 * time.Hour)

	// Total requests
	_ = s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM request_logs WHERE ts >= $1`, since,
	).Scan(&stats.TotalRequests)

	// Active users (distinct user_id)
	_ = s.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT user_id) FROM request_logs WHERE ts >= $1 AND user_id IS NOT NULL`, since,
	).Scan(&stats.ActiveUsers)

	// Provider count
	_ = s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM providers WHERE enabled = true`,
	).Scan(&stats.ProviderCount)

	// Model count
	_ = s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM models WHERE enabled = true`,
	).Scan(&stats.ModelCount)

	// Avg latency (only successful requests)
	_ = s.pool.QueryRow(ctx,
		`SELECT COALESCE(AVG(latency_ms), 0) FROM request_logs
		 WHERE ts >= $1 AND status = 'success' AND latency_ms IS NOT NULL`, since,
	).Scan(&stats.AvgLatencyMs)

	// Success rate
	var successCount, totalCount int64
	_ = s.pool.QueryRow(ctx,
		`SELECT
			COUNT(*) FILTER (WHERE status = 'success'),
			COUNT(*)
		 FROM request_logs WHERE ts >= $1`, since,
	).Scan(&successCount, &totalCount)
	if totalCount > 0 {
		stats.SuccessRate = float64(successCount) / float64(totalCount) * 100
	}

	// Top 5 models
	rows, err := s.pool.Query(ctx, `
		SELECT
			model,
			COUNT(*) as requests,
			COALESCE(AVG(latency_ms), 0) as avg_latency,
			COUNT(*) FILTER (WHERE status = 'success') as success_count
		FROM request_logs
		WHERE ts >= $1
		GROUP BY model
		ORDER BY requests DESC
		LIMIT 5`, since)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var m ModelStats
			var successCount int64
			if err := rows.Scan(&m.Model, &m.Requests, &m.AvgLatency, &successCount); err == nil {
				if stats.TotalRequests > 0 {
					m.Percentage = float64(m.Requests) / float64(stats.TotalRequests) * 100
				}
				if m.Requests > 0 {
					m.SuccessRate = float64(successCount) / float64(m.Requests) * 100
				}
				stats.TopModels = append(stats.TopModels, m)
			}
		}
	}

	// Recent errors (last 10)
	rows, err = s.pool.Query(ctx, `
		SELECT ts, model, COALESCE(provider_id::text, ''), error_code,
		       COALESCE(error_msg, ''), status
		FROM request_logs
		WHERE ts >= $1 AND status != 'success'
		ORDER BY ts DESC
		LIMIT 10`, since)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e ErrorStat
			var ts time.Time
			if err := rows.Scan(&ts, &e.Model, &e.ProviderID, &e.ErrorCode, &e.ErrorMsg, &e.Status); err == nil {
				e.Timestamp = ts.Format(time.RFC3339)
				stats.RecentErrors = append(stats.RecentErrors, e)
			}
		}
	}

	return stats, nil
}

// GetLatencyTimeseries returns latency over time for charting.
func (s *Store) GetLatencyTimeseries(ctx context.Context, hours int) ([]TimePoint, error) {
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	rows, err := s.pool.Query(ctx, `
		SELECT
			date_trunc('hour', ts) as bucket,
			COALESCE(AVG(latency_ms), 0) as avg_latency
		FROM request_logs
		WHERE ts >= $1 AND status = 'success' AND latency_ms IS NOT NULL
		GROUP BY bucket
		ORDER BY bucket ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []TimePoint
	for rows.Next() {
		var p TimePoint
		var t time.Time
		if err := rows.Scan(&t, &p.Value); err != nil {
			return nil, err
		}
		p.Timestamp = t.Format(time.RFC3339)
		points = append(points, p)
	}
	return points, rows.Err()
}

// TimePoint represents a single data point in a time series.
type TimePoint struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}
