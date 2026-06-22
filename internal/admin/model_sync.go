package admin

import (
	"regexp"
	"strings"
)

var modelAliasPattern = regexp.MustCompile(`^[a-zA-Z0-9_.\-/]+$`)

// BulkModelChannelInput creates missing model aliases and binds them to one
// provider in a single transaction.
type BulkModelChannelInput struct {
	Items    []BulkModelChannelItem `json:"items"`
	Weight   int                    `json:"weight"`
	Priority int                    `json:"priority"`
	Enabled  bool                   `json:"enabled"`
}

// BulkModelChannelItem is one upstream model selected for local setup.
type BulkModelChannelItem struct {
	UpstreamModel string `json:"upstream_model"`
	Alias         string `json:"alias"`
	DisplayName   string `json:"display_name,omitempty"`
}

// BulkModelChannelResult summarizes a bulk provider model setup run.
type BulkModelChannelResult struct {
	CreatedModels   int                         `json:"created_models"`
	CreatedChannels int                         `json:"created_channels"`
	SkippedChannels int                         `json:"skipped_channels"`
	Items           []BulkModelChannelRowResult `json:"items"`
}

// BulkModelChannelRowResult reports what happened for one requested row.
type BulkModelChannelRowResult struct {
	Alias         string `json:"alias"`
	UpstreamModel string `json:"upstream_model"`
	Status        string `json:"status"`
	ModelID       string `json:"model_id,omitempty"`
	ChannelID     string `json:"channel_id,omitempty"`
	Error         string `json:"error,omitempty"`
}

func normalizeBulkModelChannelInput(in BulkModelChannelInput) (BulkModelChannelInput, error) {
	out := BulkModelChannelInput{
		Items:    make([]BulkModelChannelItem, 0, len(in.Items)),
		Weight:   nz(in.Weight, 1),
		Priority: in.Priority,
		Enabled:  in.Enabled,
	}
	if len(in.Items) == 0 {
		return out, ErrValidation
	}
	for _, item := range in.Items {
		item.UpstreamModel = strings.TrimSpace(item.UpstreamModel)
		item.Alias = strings.TrimSpace(item.Alias)
		item.DisplayName = strings.TrimSpace(item.DisplayName)
		if item.UpstreamModel == "" || item.Alias == "" || !modelAliasPattern.MatchString(item.Alias) {
			return out, ErrValidation
		}
		if item.DisplayName == "" {
			item.DisplayName = item.Alias
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
