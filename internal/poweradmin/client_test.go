package poweradmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if err := client.DeleteRecord(context.Background(), 1, "999"); err != nil {
		t.Errorf("expected nil (idempotent) for 404 on delete, got: %v", err)
	}
}

// A 2xx delete response carrying success=false is a failure, not a success:
// the record is still there.
func TestDeleteRecord_SuccessFalseIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"Record is locked"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", APIVersionV2)
	err := client.DeleteRecord(context.Background(), 1, "999")
	if err == nil {
		t.Fatal("expected error for 200 response with success=false")
	}
	if !strings.Contains(err.Error(), "Record is locked") {
		t.Errorf("expected API message in error, got: %v", err)
	}
}

// A 204 No Content delete (empty body) is a success.
func TestDeleteRecord_NoContentIsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", APIVersionV2)
	if err := client.DeleteRecord(context.Background(), 1, "999"); err != nil {
		t.Errorf("expected nil for 204 delete, got: %v", err)
	}
}

// A trailing slash in the configured base URL must not produce double-slash
// request paths.
func TestNewClient_TrimsTrailingSlash(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"success":true,"data":{"zones":[]}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", APIVersionV2)
	if _, err := client.ListZones(context.Background()); err != nil {
		t.Fatalf("ListZones failed: %v", err)
	}
	if gotPath != "/api/v2/zones" {
		t.Errorf("expected path /api/v2/zones, got %q", gotPath)
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
	err := client.DeleteRecord(context.Background(), 1, "999")
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

// The v1 API is PHP-backed and returns disabled inconsistently (bool, int,
// string); the create response must tolerate every form, same as FlexBool
// does for record listings.
func TestCreateRecordV1_DisabledAsBool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"success":true,"data":{"record_id":42,"name":"www","type":"A","content":"1.1.1.1","ttl":300,"disabled":false}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", APIVersionV1)
	record, err := client.CreateRecord(context.Background(), 1, CreateRecordRequest{
		Name: "www", Type: "A", Content: "1.1.1.1", TTL: 300,
	})
	if err != nil {
		t.Fatalf("CreateRecord failed on bool disabled in v1 response: %v", err)
	}
	if record.ID != "42" {
		t.Errorf("expected record ID 42, got %s", record.ID)
	}
	if bool(record.Disabled) {
		t.Error("expected disabled=false")
	}
}

// PowerAdmin's API/agent DNS backend (4.3.0+) synthesizes opaque string record
// IDs, so listing must decode string IDs and mutations must send them verbatim.
func TestRecordID_StringIDsRoundTrip(t *testing.T) {
	const stringID = "eyJ6IjoiZXhhbXBsZS5jb20ifQ"
	var gotPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"data":{"records":[{"id":"` + stringID + `","zone_id":1,"name":"www.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":false}]}}`))
		case http.MethodPut:
			gotPaths = append(gotPaths, r.URL.Path)
			_, _ = w.Write([]byte(`{"success":true,"data":{"record":{"id":"` + stringID + `","zone_id":1,"name":"www.example.com","type":"A","content":"2.2.2.2","ttl":300,"disabled":false}}}`))
		case http.MethodDelete:
			gotPaths = append(gotPaths, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", APIVersionV2)

	records, err := client.ListRecords(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecords failed on string record ID: %v", err)
	}
	if len(records) != 1 || records[0].ID != stringID {
		t.Fatalf("expected 1 record with ID %q, got %+v", stringID, records)
	}

	if _, err := client.UpdateRecord(context.Background(), 1, records[0].ID, UpdateRecordRequest{Content: "2.2.2.2"}); err != nil {
		t.Fatalf("UpdateRecord failed: %v", err)
	}
	if err := client.DeleteRecord(context.Background(), 1, records[0].ID); err != nil {
		t.Fatalf("DeleteRecord failed: %v", err)
	}

	want := "/api/v2/zones/1/records/" + stringID
	for _, p := range gotPaths {
		if p != want {
			t.Errorf("expected mutation path %q, got %q", want, p)
		}
	}
	if len(gotPaths) != 2 {
		t.Errorf("expected 2 mutation calls, got %d", len(gotPaths))
	}
}

// Numeric IDs remain the common case; they must keep decoding from JSON numbers.
func TestRecordID_NumericIDsStillDecode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"records":[{"id":101,"zone_id":1,"name":"www.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":false}]}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", APIVersionV2)
	records, err := client.ListRecords(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecords failed on numeric record ID: %v", err)
	}
	if len(records) != 1 || records[0].ID != "101" {
		t.Fatalf("expected 1 record with ID \"101\", got %+v", records)
	}
}
