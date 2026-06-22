package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAdminDuplicateDefaultProfileReturnsValidation(t *testing.T) {
	_, _, app, _ := setup(t)

	body := `{"name":"default-profile","scope":"default","headers":{"x-app":"cli"},"enabled":true}`
	resp, b := do(t, app, http.MethodPost, "/api/admin/client-profiles", body, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", resp.StatusCode, b)
	}

	resp, b = do(t, app, http.MethodPost, "/api/admin/client-profiles", body, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("second create status=%d body=%s", resp.StatusCode, b)
	}
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json: %v body=%s", err, b)
	}
	if out.Error.Code != "validation_error" || out.Error.Message == "" {
		t.Fatalf("unexpected error body: %+v", out.Error)
	}
}
