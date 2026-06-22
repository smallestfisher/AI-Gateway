package admin

import (
	"errors"
	"testing"
)

func TestNormalizeBulkModelChannelInputTrimsDefaultsAndValidatesAlias(t *testing.T) {
	got, err := normalizeBulkModelChannelInput(BulkModelChannelInput{
		Items: []BulkModelChannelItem{
			{UpstreamModel: " gpt-4o-mini ", Alias: " gpt-4o-mini "},
		},
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Weight != 1 || got.Items[0].UpstreamModel != "gpt-4o-mini" || got.Items[0].DisplayName != "gpt-4o-mini" {
		t.Fatalf("normalized input mismatch: %+v", got)
	}

	_, err = normalizeBulkModelChannelInput(BulkModelChannelInput{
		Items: []BulkModelChannelItem{{UpstreamModel: "bad", Alias: "bad alias"}},
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}
