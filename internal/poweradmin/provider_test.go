package poweradmin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

// mockServer creates a test server that tracks API calls
type mockServer struct {
	server      *httptest.Server
	zones       []Zone
	records     map[int][]Record // zoneID -> records
	createCalls []CreateRecordRequest
	updateCalls []updateCall
	deleteCalls []deleteCall
}

type updateCall struct {
	zoneID   int
	recordID int
	request  UpdateRecordRequest
}

type deleteCall struct {
	zoneID   int
	recordID int
}

func newMockServer(zones []Zone, records map[int][]Record) *mockServer {
	ms := &mockServer{
		zones:   zones,
		records: records,
	}

	mux := http.NewServeMux()

	// List zones
	mux.HandleFunc("/api/v2/zones", func(w http.ResponseWriter, r *http.Request) {
		resp := ZonesResponse{Success: true, Data: ms.zones}
		json.NewEncoder(w).Encode(resp)
	})

	// Zone records - handles all /api/v2/zones/{id}/* paths
	mux.HandleFunc("/api/v2/zones/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Parse zone ID and optional record ID
		var zoneID, recordID int
		hasRecordID := false

		if strings.Contains(path, "/records/") {
			// /api/v2/zones/{zoneID}/records/{recordID}
			fmt.Sscanf(path, "/api/v2/zones/%d/records/%d", &zoneID, &recordID)
			hasRecordID = true
		} else if strings.HasSuffix(path, "/records") {
			// /api/v2/zones/{zoneID}/records
			fmt.Sscanf(path, "/api/v2/zones/%d/records", &zoneID)
		} else {
			// /api/v2/zones/{zoneID}
			fmt.Sscanf(path, "/api/v2/zones/%d", &zoneID)
		}

		switch r.Method {
		case http.MethodGet:
			if recs, ok := ms.records[zoneID]; ok {
				resp := RecordsResponse{Success: true, Data: recs}
				json.NewEncoder(w).Encode(resp)
			} else {
				resp := RecordsResponse{Success: true, Data: []Record{}}
				json.NewEncoder(w).Encode(resp)
			}

		case http.MethodPost:
			var req CreateRecordRequest
			json.NewDecoder(r.Body).Decode(&req)
			ms.createCalls = append(ms.createCalls, req)

			newID := len(ms.records[zoneID]) + 100
			newRecord := Record{
				ID:      newID,
				ZoneID:  zoneID,
				Name:    req.Name,
				Type:    req.Type,
				Content: req.Content,
				TTL:     req.TTL,
			}
			resp := RecordResponse{Success: true}
			resp.Data.Record = newRecord
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(resp)

		case http.MethodPut:
			if hasRecordID {
				var req UpdateRecordRequest
				json.NewDecoder(r.Body).Decode(&req)
				ms.updateCalls = append(ms.updateCalls, updateCall{
					zoneID:   zoneID,
					recordID: recordID,
					request:  req,
				})
				resp := RecordResponse{Success: true}
				json.NewEncoder(w).Encode(resp)
			}

		case http.MethodDelete:
			if hasRecordID {
				ms.deleteCalls = append(ms.deleteCalls, deleteCall{
					zoneID:   zoneID,
					recordID: recordID,
				})
				w.WriteHeader(http.StatusNoContent)
			}
		}
	})

	ms.server = httptest.NewServer(mux)
	return ms
}

func (ms *mockServer) Close() {
	ms.server.Close()
}

func TestUpdateRecord_MultipleTargets(t *testing.T) {
	// Setup: zone with two A records for same hostname
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www", Type: "A", Content: "2.2.2.2", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache["example.com"] = zones[0]

	// Update both targets to new values
	oldEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1", "2.2.2.2"},
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"3.3.3.3", "4.4.4.4"},
	}

	err = provider.updateRecord(context.Background(), oldEp, newEp)
	if err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	// Verify: should have 2 update calls with different content
	if len(ms.updateCalls) != 2 {
		t.Errorf("Expected 2 update calls, got %d", len(ms.updateCalls))
	}

	contents := make(map[string]bool)
	for _, call := range ms.updateCalls {
		contents[call.request.Content] = true
	}

	if !contents["3.3.3.3"] {
		t.Error("Expected update to 3.3.3.3")
	}
	if !contents["4.4.4.4"] {
		t.Error("Expected update to 4.4.4.4")
	}
}

func TestUpdateRecord_DuplicateTargets(t *testing.T) {
	// Setup: zone with two A records with SAME content (duplicate targets)
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www", Type: "A", Content: "1.1.1.1", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache["example.com"] = zones[0]

	// Update duplicate targets to different new values
	oldEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1", "1.1.1.1"},
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1", "2.2.2.2"},
	}

	err = provider.updateRecord(context.Background(), oldEp, newEp)
	if err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	// Verify: should have 2 update calls - one for each record
	if len(ms.updateCalls) != 2 {
		t.Errorf("Expected 2 update calls, got %d", len(ms.updateCalls))
	}

	// Verify different record IDs were updated
	recordIDs := make(map[int]bool)
	for _, call := range ms.updateCalls {
		recordIDs[call.recordID] = true
	}

	if len(recordIDs) != 2 {
		t.Errorf("Expected 2 different record IDs to be updated, got %d", len(recordIDs))
	}

	// Verify both new contents are present
	contents := make(map[string]bool)
	for _, call := range ms.updateCalls {
		contents[call.request.Content] = true
	}

	if !contents["1.1.1.1"] {
		t.Error("Expected one update to keep 1.1.1.1")
	}
	if !contents["2.2.2.2"] {
		t.Error("Expected one update to 2.2.2.2")
	}
}

func TestApplyChanges_CreateMultipleTargets(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{1: {}}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "www.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"1.1.1.1", "2.2.2.2"},
				RecordTTL:  300,
			},
		},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	// Verify: should have 2 create calls
	if len(ms.createCalls) != 2 {
		t.Errorf("Expected 2 create calls, got %d", len(ms.createCalls))
	}

	contents := make(map[string]bool)
	for _, call := range ms.createCalls {
		contents[call.Content] = true
	}

	if !contents["1.1.1.1"] || !contents["2.2.2.2"] {
		t.Error("Expected creates for both 1.1.1.1 and 2.2.2.2")
	}
}

func TestExtractRecordName(t *testing.T) {
	tests := []struct {
		dnsName  string
		zoneName string
		expected string
	}{
		{"example.com", "example.com", "@"},
		{"www.example.com", "example.com", "www"},
		{"sub.www.example.com", "example.com", "sub.www"},
		{"test.sub.example.com", "sub.example.com", "test"},
	}

	for _, tt := range tests {
		result := extractRecordName(tt.dnsName, tt.zoneName)
		if result != tt.expected {
			t.Errorf("extractRecordName(%q, %q) = %q, want %q",
				tt.dnsName, tt.zoneName, result, tt.expected)
		}
	}
}

func TestParseTarget_MX(t *testing.T) {
	content, priority := parseTarget("MX", "10 mail.example.com")

	if content != "mail.example.com" {
		t.Errorf("Expected content 'mail.example.com', got %q", content)
	}
	if priority == nil || *priority != 10 {
		t.Errorf("Expected priority 10, got %v", priority)
	}
}

func TestParseTarget_TXT(t *testing.T) {
	content, priority := parseTarget("TXT", "\"v=spf1 include:example.com ~all\"")

	if content != "v=spf1 include:example.com ~all" {
		t.Errorf("Expected unquoted content, got %q", content)
	}
	if priority != nil {
		t.Error("Expected nil priority for TXT record")
	}
}

func TestParseTarget_A(t *testing.T) {
	content, priority := parseTarget("A", "192.168.1.1")

	if content != "192.168.1.1" {
		t.Errorf("Expected content '192.168.1.1', got %q", content)
	}
	if priority != nil {
		t.Error("Expected nil priority for A record")
	}
}

func TestIsSupportedRecordType(t *testing.T) {
	supported := []string{"A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "PTR", "CAA"}
	unsupported := []string{"SOA", "DNSKEY", "DS", "RRSIG", "NSEC"}

	for _, rt := range supported {
		if !isSupportedRecordType(rt) {
			t.Errorf("Expected %s to be supported", rt)
		}
	}

	for _, rt := range unsupported {
		if isSupportedRecordType(rt) {
			t.Errorf("Expected %s to be unsupported", rt)
		}
	}
}

func TestNewProvider_Validation(t *testing.T) {
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})

	// Missing URL
	_, err := NewProvider("", "api-key", domainFilter, false)
	if err == nil {
		t.Error("Expected error for missing URL")
	}

	// Missing API key
	_, err = NewProvider("http://example.com", "", domainFilter, false)
	if err == nil {
		t.Error("Expected error for missing API key")
	}

	// Valid config
	_, err = NewProvider("http://example.com", "api-key", domainFilter, false)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestGetDomainFilter(t *testing.T) {
	domainFilter := endpoint.NewDomainFilter([]string{"example.com", "test.com"})
	provider, err := NewProvider("http://example.com", "api-key", domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	filter := provider.GetDomainFilter()
	if filter == nil {
		t.Fatal("Expected non-nil domain filter")
	}

	if !filter.Match("example.com") {
		t.Error("Expected example.com to match")
	}
	if !filter.Match("sub.example.com") {
		t.Error("Expected sub.example.com to match")
	}
	if !filter.Match("test.com") {
		t.Error("Expected test.com to match")
	}
}

func TestAdjustEndpoints(t *testing.T) {
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider("http://example.com", "api-key", domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints := []*endpoint.Endpoint{
		{DNSName: "www.example.com", RecordType: "A", Targets: endpoint.Targets{"1.1.1.1"}},
		{DNSName: "mail.example.com", RecordType: "MX", Targets: endpoint.Targets{"10 mx.example.com"}},
	}

	adjusted, err := provider.AdjustEndpoints(endpoints)
	if err != nil {
		t.Fatalf("AdjustEndpoints failed: %v", err)
	}

	// Should return endpoints unchanged
	if len(adjusted) != len(endpoints) {
		t.Errorf("Expected %d endpoints, got %d", len(endpoints), len(adjusted))
	}
}
