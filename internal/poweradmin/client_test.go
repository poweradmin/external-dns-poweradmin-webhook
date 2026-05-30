package poweradmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A 404 on delete means the record is already gone; re-applying the desired
// state must stay idempotent, so DeleteRecord treats it as success.
func TestDeleteRecord_NotFoundIsIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"message":"Record not found in this zone"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", APIVersionV2)
	if err := client.DeleteRecord(context.Background(), 1, 999); err != nil {
		t.Errorf("expected nil (idempotent) for 404 on delete, got: %v", err)
	}
}

// Non-404 failures surface as a typed *APIError carrying the status code and the
// structured API "message" field.
func TestDeleteRecord_ServerErrorReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"message":"Failed to delete record"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", APIVersionV2)
	err := client.DeleteRecord(context.Background(), 1, 999)
	if err == nil {
		t.Fatal("expected error for 500 on delete")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "Failed to delete record" {
		t.Errorf("expected parsed API message, got %q", apiErr.Message)
	}
}
