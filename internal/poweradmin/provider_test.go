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

// Note: Test records use full DNS names (e.g., "www.example.com") to match
// the PowerAdmin API response format.

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
		resp := ZonesResponseV2{Success: true}
		resp.Data.Zones = ms.zones
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Zone records - handles all /api/v2/zones/{id}/* paths
	mux.HandleFunc("/api/v2/zones/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Parse zone ID and optional record ID
		var zoneID, recordID int
		hasRecordID := false

		if strings.Contains(path, "/records/") {
			// /api/v2/zones/{zoneID}/records/{recordID}
			_, _ = fmt.Sscanf(path, "/api/v2/zones/%d/records/%d", &zoneID, &recordID)
			hasRecordID = true
		} else if strings.HasSuffix(path, "/records") {
			// /api/v2/zones/{zoneID}/records
			_, _ = fmt.Sscanf(path, "/api/v2/zones/%d/records", &zoneID)
		} else {
			// /api/v2/zones/{zoneID}
			_, _ = fmt.Sscanf(path, "/api/v2/zones/%d", &zoneID)
		}

		switch r.Method {
		case http.MethodGet:
			if recs, ok := ms.records[zoneID]; ok {
				resp := RecordsResponse{Success: true, Data: recs}
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				resp := RecordsResponse{Success: true, Data: []Record{}}
				_ = json.NewEncoder(w).Encode(resp)
			}

		case http.MethodPost:
			var req CreateRecordRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
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
			resp := RecordResponseV2{Success: true}
			resp.Data.Record = newRecord
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)

		case http.MethodPut:
			if hasRecordID {
				var req UpdateRecordRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				ms.updateCalls = append(ms.updateCalls, updateCall{
					zoneID:   zoneID,
					recordID: recordID,
					request:  req,
				})
				resp := RecordResponseV2{Success: true}
				_ = json.NewEncoder(w).Encode(resp)
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
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "2.2.2.2", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
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
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
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
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
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
	// Input with quotes should be normalized and re-quoted
	content, priority := parseTarget("TXT", "\"v=spf1 include:example.com ~all\"")
	if content != "\"v=spf1 include:example.com ~all\"" {
		t.Errorf("Expected quoted content, got %q", content)
	}
	if priority != nil {
		t.Error("Expected nil priority for TXT record")
	}

	// Input without quotes should get quotes added
	content2, _ := parseTarget("TXT", "v=spf1 include:example.com ~all")
	if content2 != "\"v=spf1 include:example.com ~all\"" {
		t.Errorf("Expected quoted content, got %q", content2)
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
	_, err := NewProvider("", "api-key", APIVersionV2, domainFilter, false)
	if err == nil {
		t.Error("Expected error for missing URL")
	}

	// Missing API key
	_, err = NewProvider("http://example.com", "", APIVersionV2, domainFilter, false)
	if err == nil {
		t.Error("Expected error for missing API key")
	}

	// Valid config
	_, err = NewProvider("http://example.com", "api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestGetDomainFilter(t *testing.T) {
	domainFilter := endpoint.NewDomainFilter([]string{"example.com", "test.com"})
	provider, err := NewProvider("http://example.com", "api-key", APIVersionV2, domainFilter, false)
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
	provider, err := NewProvider("http://example.com", "api-key", APIVersionV2, domainFilter, false)
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

// TestApplyChanges_NoChanges verifies that empty changes don't cause errors
func TestApplyChanges_NoChanges(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{1: {}}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Empty changes
	changes := &plan.Changes{
		Create:    []*endpoint.Endpoint{},
		Delete:    []*endpoint.Endpoint{},
		UpdateNew: []*endpoint.Endpoint{},
		UpdateOld: []*endpoint.Endpoint{},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	if err != nil {
		t.Errorf("ApplyChanges with no changes should not error: %v", err)
	}

	// Verify no API calls were made
	if len(ms.createCalls) != 0 || len(ms.updateCalls) != 0 || len(ms.deleteCalls) != 0 {
		t.Error("Expected no API calls for empty changes")
	}
}

// TestApplyChanges_Delete verifies delete operations
func TestApplyChanges_Delete(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "api.example.com", Type: "A", Content: "2.2.2.2", TTL: 300},
			{ID: 103, ZoneID: 1, Name: "example.com", Type: "CNAME", Content: "www.example.com", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{
			{
				DNSName:    "www.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"1.1.1.1"},
			},
			{
				DNSName:    "example.com",
				RecordType: "CNAME",
				Targets:    endpoint.Targets{"www.example.com"},
			},
		},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	// Verify: should have 2 delete calls
	if len(ms.deleteCalls) != 2 {
		t.Errorf("Expected 2 delete calls, got %d", len(ms.deleteCalls))
	}

	// Verify correct record IDs were deleted
	deletedIDs := make(map[int]bool)
	for _, call := range ms.deleteCalls {
		deletedIDs[call.recordID] = true
	}

	if !deletedIDs[101] {
		t.Error("Expected record 101 (www A) to be deleted")
	}
	if !deletedIDs[103] {
		t.Error("Expected record 103 (@ CNAME) to be deleted")
	}
	if deletedIDs[102] {
		t.Error("Record 102 (api A) should NOT be deleted")
	}
}

// TestApplyChanges_DryRun verifies dry-run mode doesn't make API calls
func TestApplyChanges_DryRun(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	// Enable dry-run mode
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, true)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "new.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"3.3.3.3"},
				RecordTTL:  300,
			},
		},
		Delete: []*endpoint.Endpoint{
			{
				DNSName:    "www.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"1.1.1.1"},
			},
		},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ApplyChanges in dry-run mode failed: %v", err)
	}

	// Verify no actual API calls were made (only zone list is called)
	if len(ms.createCalls) != 0 {
		t.Errorf("Expected 0 create calls in dry-run, got %d", len(ms.createCalls))
	}
	if len(ms.deleteCalls) != 0 {
		t.Errorf("Expected 0 delete calls in dry-run, got %d", len(ms.deleteCalls))
	}
	if len(ms.updateCalls) != 0 {
		t.Errorf("Expected 0 update calls in dry-run, got %d", len(ms.updateCalls))
	}
}

// TestRecords_FiltersByDomain verifies domain filtering
func TestRecords_FiltersByDomain(t *testing.T) {
	zones := []Zone{
		{ID: 1, Name: "example.com"},
		{ID: 2, Name: "other.org"},
	}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
		},
		2: {
			{ID: 201, ZoneID: 2, Name: "www.other.org", Type: "A", Content: "2.2.2.2", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	// Only filter for example.com
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed: %v", err)
	}

	// Should only return records from example.com
	if len(endpoints) != 1 {
		t.Errorf("Expected 1 endpoint, got %d", len(endpoints))
	}

	if len(endpoints) > 0 && endpoints[0].DNSName != "www.example.com" {
		t.Errorf("Expected www.example.com, got %s", endpoints[0].DNSName)
	}
}

// TestRecords_EmptyZone verifies handling of zones with no records
func TestRecords_EmptyZone(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{1: {}}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed: %v", err)
	}

	if len(endpoints) != 0 {
		t.Errorf("Expected 0 endpoints for empty zone, got %d", len(endpoints))
	}
}

// TestRecords_SkipsSOAandNS verifies SOA and apex NS records are skipped
func TestRecords_SkipsSOAandNS(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "example.com", Type: "SOA", Content: "ns1.example.com hostmaster.example.com 2021010101 3600 600 604800 86400", TTL: 3600},
			{ID: 102, ZoneID: 1, Name: "example.com", Type: "NS", Content: "ns1.example.com", TTL: 3600},
			{ID: 103, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 104, ZoneID: 1, Name: "sub.example.com", Type: "NS", Content: "ns1.sub.example.com", TTL: 3600}, // delegated NS, should be included
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed: %v", err)
	}

	// Should only return www A record and sub NS record (not SOA, not apex NS)
	if len(endpoints) != 2 {
		t.Errorf("Expected 2 endpoints (www A + sub NS), got %d", len(endpoints))
	}

	for _, ep := range endpoints {
		if ep.RecordType == "SOA" {
			t.Error("SOA record should be skipped")
		}
		if ep.RecordType == "NS" && ep.DNSName == "example.com" {
			t.Error("Apex NS record should be skipped")
		}
	}
}

// TestFindZoneForEndpoint verifies longest suffix matching
func TestFindZoneForEndpoint(t *testing.T) {
	zones := []Zone{
		{ID: 1, Name: "example.com"},
		{ID: 2, Name: "sub.example.com"},
	}

	domainFilter := endpoint.NewDomainFilter([]string{"example.com", "sub.example.com"})
	provider, err := NewProvider("http://example.com", "api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Populate zone cache
	for _, z := range zones {
		provider.zoneCache[z.Name] = z
	}

	tests := []struct {
		dnsName      string
		expectedZone string
	}{
		{"www.example.com", "example.com"},
		{"api.example.com", "example.com"},
		{"test.sub.example.com", "sub.example.com"},
		{"deep.test.sub.example.com", "sub.example.com"},
	}

	for _, tt := range tests {
		ep := &endpoint.Endpoint{DNSName: tt.dnsName}
		zone, err := provider.findZoneForEndpoint(ep)
		if err != nil {
			t.Errorf("findZoneForEndpoint(%s) error: %v", tt.dnsName, err)
			continue
		}
		if zone.Name != tt.expectedZone {
			t.Errorf("findZoneForEndpoint(%s) = %s, want %s", tt.dnsName, zone.Name, tt.expectedZone)
		}
	}
}

// TestFindZoneForEndpoint_NoMatch verifies error when no zone matches
func TestFindZoneForEndpoint_NoMatch(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider("http://example.com", "api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	provider.zoneCache["example.com"] = zones[0]

	ep := &endpoint.Endpoint{DNSName: "www.other.org"}
	_, err = provider.findZoneForEndpoint(ep)
	if err == nil {
		t.Error("Expected error for non-matching domain")
	}
}

// TestRecords_MXWithPriority verifies MX records include priority in target
func TestRecords_MXWithPriority(t *testing.T) {
	priority := 10
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "example.com", Type: "MX", Content: "mail.example.com", TTL: 300, Priority: &priority},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed: %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(endpoints))
	}

	// MX target should include priority
	expected := "10 mail.example.com"
	if endpoints[0].Targets[0] != expected {
		t.Errorf("Expected MX target %q, got %q", expected, endpoints[0].Targets[0])
	}
}

// TestApplyChanges_MixedOperations verifies create, update, and delete in single call
func TestApplyChanges_MixedOperations(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "old.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "update.example.com", Type: "A", Content: "2.2.2.2", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{DNSName: "new.example.com", RecordType: "A", Targets: endpoint.Targets{"3.3.3.3"}, RecordTTL: 300},
		},
		UpdateOld: []*endpoint.Endpoint{
			{DNSName: "update.example.com", RecordType: "A", Targets: endpoint.Targets{"2.2.2.2"}},
		},
		UpdateNew: []*endpoint.Endpoint{
			{DNSName: "update.example.com", RecordType: "A", Targets: endpoint.Targets{"4.4.4.4"}, RecordTTL: 300},
		},
		Delete: []*endpoint.Endpoint{
			{DNSName: "old.example.com", RecordType: "A", Targets: endpoint.Targets{"1.1.1.1"}},
		},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Errorf("Expected 1 create call, got %d", len(ms.createCalls))
	}
	if len(ms.updateCalls) != 1 {
		t.Errorf("Expected 1 update call, got %d", len(ms.updateCalls))
	}
	if len(ms.deleteCalls) != 1 {
		t.Errorf("Expected 1 delete call, got %d", len(ms.deleteCalls))
	}
}

// newMockServerV1 creates a test server that responds with V1 API format
func newMockServerV1(zones []Zone, records map[int][]Record) *mockServer {
	ms := &mockServer{
		zones:   zones,
		records: records,
	}

	mux := http.NewServeMux()

	// List zones - V1 returns array directly in data
	mux.HandleFunc("/api/v1/zones", func(w http.ResponseWriter, r *http.Request) {
		resp := ZonesResponseV1{Success: true, Data: ms.zones}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Zone records - handles all /api/v1/zones/{id}/* paths
	mux.HandleFunc("/api/v1/zones/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Parse zone ID and optional record ID
		var zoneID, recordID int
		hasRecordID := false

		if strings.Contains(path, "/records/") {
			// /api/v1/zones/{zoneID}/records/{recordID}
			_, _ = fmt.Sscanf(path, "/api/v1/zones/%d/records/%d", &zoneID, &recordID)
			hasRecordID = true
		} else if strings.HasSuffix(path, "/records") {
			// /api/v1/zones/{zoneID}/records
			_, _ = fmt.Sscanf(path, "/api/v1/zones/%d/records", &zoneID)
		} else {
			// /api/v1/zones/{zoneID}
			_, _ = fmt.Sscanf(path, "/api/v1/zones/%d", &zoneID)
		}

		switch r.Method {
		case http.MethodGet:
			// V1 API returns disabled as integer (0/1), so we build raw JSON
			// to simulate real API behavior instead of using RecordsResponse
			// which would serialize FlexBool as a boolean.
			if recs, ok := ms.records[zoneID]; ok {
				type v1Record struct {
					ID       int    `json:"id"`
					ZoneID   int    `json:"zone_id"`
					Name     string `json:"name"`
					Type     string `json:"type"`
					Content  string `json:"content"`
					TTL      int    `json:"ttl"`
					Priority *int   `json:"priority,omitempty"`
					Disabled int    `json:"disabled"`
				}
				var v1Recs []v1Record
				for _, r := range recs {
					d := 0
					if r.Disabled {
						d = 1
					}
					v1Recs = append(v1Recs, v1Record{
						ID: r.ID, ZoneID: r.ZoneID, Name: r.Name,
						Type: r.Type, Content: r.Content, TTL: r.TTL,
						Priority: r.Priority, Disabled: d,
					})
				}
				resp := struct {
					Success bool       `json:"success"`
					Message string     `json:"message"`
					Data    []v1Record `json:"data"`
				}{Success: true, Data: v1Recs}
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				w.Write([]byte(`{"success":true,"data":[]}`))
			}

		case http.MethodPost:
			var req CreateRecordRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			ms.createCalls = append(ms.createCalls, req)

			newID := len(ms.records[zoneID]) + 100
			// V1 returns flat structure with record_id
			resp := RecordResponseV1{Success: true}
			resp.Data.RecordID = newID
			resp.Data.Name = req.Name
			resp.Data.Type = req.Type
			resp.Data.Content = req.Content
			resp.Data.TTL = req.TTL
			resp.Data.Priority = req.Priority
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)

		case http.MethodPut:
			if hasRecordID {
				var req UpdateRecordRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				ms.updateCalls = append(ms.updateCalls, updateCall{
					zoneID:   zoneID,
					recordID: recordID,
					request:  req,
				})
				// V1 returns null data on update
				resp := APIResponse{Success: true, Message: "Record updated successfully"}
				_ = json.NewEncoder(w).Encode(resp)
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

// TestV1API_ListZones verifies V1 API zone listing
func TestV1API_ListZones(t *testing.T) {
	zones := []Zone{
		{ID: 1, Name: "example.com"},
		{ID: 2, Name: "test.org"},
	}
	records := map[int][]Record{1: {}, 2: {}}

	ms := newMockServerV1(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com", "test.org"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV1, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed: %v", err)
	}

	// Should have loaded both zones
	if len(provider.zoneCache) != 2 {
		t.Errorf("Expected 2 zones in cache, got %d", len(provider.zoneCache))
	}

	// No records, so no endpoints
	if len(endpoints) != 0 {
		t.Errorf("Expected 0 endpoints, got %d", len(endpoints))
	}
}

// TestV1API_CreateRecord verifies V1 API record creation
func TestV1API_CreateRecord(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{1: {}}

	ms := newMockServerV1(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV1, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "www.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"1.1.1.1"},
				RecordTTL:  300,
			},
		},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Errorf("Expected 1 create call, got %d", len(ms.createCalls))
	}

	if ms.createCalls[0].Content != "1.1.1.1" {
		t.Errorf("Expected content '1.1.1.1', got %q", ms.createCalls[0].Content)
	}
}

// TestV1API_UpdateRecord verifies V1 API record update
func TestV1API_UpdateRecord(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
		},
	}

	ms := newMockServerV1(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV1, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache["example.com"] = zones[0]

	oldEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1"},
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"2.2.2.2"},
		RecordTTL:  600,
	}

	err = provider.updateRecord(context.Background(), oldEp, newEp)
	if err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	if len(ms.updateCalls) != 1 {
		t.Errorf("Expected 1 update call, got %d", len(ms.updateCalls))
	}

	if ms.updateCalls[0].request.Content != "2.2.2.2" {
		t.Errorf("Expected content '2.2.2.2', got %q", ms.updateCalls[0].request.Content)
	}
}

// TestV1API_DeleteRecord verifies V1 API record deletion
func TestV1API_DeleteRecord(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
		},
	}

	ms := newMockServerV1(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV1, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{
			{
				DNSName:    "www.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"1.1.1.1"},
			},
		},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if len(ms.deleteCalls) != 1 {
		t.Errorf("Expected 1 delete call, got %d", len(ms.deleteCalls))
	}

	if ms.deleteCalls[0].recordID != 101 {
		t.Errorf("Expected record ID 101, got %d", ms.deleteCalls[0].recordID)
	}
}

// TestAPIVersionDefault verifies default API version is V2
func TestAPIVersionDefault(t *testing.T) {
	client := NewClient("http://example.com", "api-key", "")
	if client.apiVersion != APIVersionV2 {
		t.Errorf("Expected default API version to be V2, got %s", client.apiVersion)
	}
}

// TestFlexBool_UnmarshalJSON verifies FlexBool handles both bool and int JSON values
func TestFlexBool_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected FlexBool
		wantErr  bool
	}{
		{"bool true", "true", FlexBool(true), false},
		{"bool false", "false", FlexBool(false), false},
		{"int 1", "1", FlexBool(true), false},
		{"int 0", "0", FlexBool(false), false},
		{"invalid", "\"yes\"", FlexBool(false), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b FlexBool
			err := json.Unmarshal([]byte(tt.input), &b)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON(%s) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && b != tt.expected {
				t.Errorf("UnmarshalJSON(%s) = %v, want %v", tt.input, b, tt.expected)
			}
		})
	}
}

// TestRecords_TXTQuoting verifies TXT records are properly unquoted on read
func TestRecords_TXTQuoting(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "example.com", Type: "TXT", Content: "\"v=spf1 include:example.com ~all\"", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "example.com", Type: "TXT", Content: "unquoted-value", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed: %v", err)
	}

	if len(endpoints) != 2 {
		t.Fatalf("Expected 2 endpoints, got %d", len(endpoints))
	}

	// Quoted TXT content should have quotes stripped
	for _, ep := range endpoints {
		target := ep.Targets[0]
		if strings.HasPrefix(target, "\"") || strings.HasSuffix(target, "\"") {
			t.Errorf("TXT target should be unquoted, got %q", target)
		}
	}
}

// TestV1API_ListRecords_DisabledAsInt verifies V1 records with int disabled field are parsed correctly
func TestV1API_ListRecords_DisabledAsInt(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300, Disabled: FlexBool(false)},
			{ID: 102, ZoneID: 1, Name: "disabled.example.com", Type: "A", Content: "2.2.2.2", TTL: 300, Disabled: FlexBool(true)},
		},
	}

	ms := newMockServerV1(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV1, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// The V1 mock returns disabled as int (0/1). This should parse without error.
	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed (disabled as int should be handled): %v", err)
	}

	if len(endpoints) != 2 {
		t.Errorf("Expected 2 endpoints, got %d", len(endpoints))
	}
}

// TestUpdateRecord_TXTUnquotedContent verifies TXT update/delete works when API returns unquoted content
func TestUpdateRecord_TXTUnquotedContent(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			// API returns unquoted TXT content
			{ID: 101, ZoneID: 1, Name: "example.com", Type: "TXT", Content: "v=spf1 include:example.com ~all", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache["example.com"] = zones[0]

	// external-dns stores unquoted targets (Records() strips quotes)
	oldEp := &endpoint.Endpoint{
		DNSName:    "example.com",
		RecordType: "TXT",
		Targets:    endpoint.Targets{"v=spf1 include:example.com ~all"},
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "example.com",
		RecordType: "TXT",
		Targets:    endpoint.Targets{"v=spf1 include:new.com ~all"},
		RecordTTL:  300,
	}

	err = provider.updateRecord(context.Background(), oldEp, newEp)
	if err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	if len(ms.updateCalls) != 1 {
		t.Fatalf("Expected 1 update call, got %d (TXT matching failed with unquoted API content)", len(ms.updateCalls))
	}

	// The content sent to the API should be quoted
	if ms.updateCalls[0].request.Content != "\"v=spf1 include:new.com ~all\"" {
		t.Errorf("Expected quoted content sent to API, got %q", ms.updateCalls[0].request.Content)
	}
}

// TestDeleteRecord_TXTUnquotedContent verifies TXT delete works when API returns unquoted content
func TestDeleteRecord_TXTUnquotedContent(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "example.com", Type: "TXT", Content: "v=spf1 include:example.com ~all", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache["example.com"] = zones[0]

	err = provider.deleteRecord(context.Background(), &endpoint.Endpoint{
		DNSName:    "example.com",
		RecordType: "TXT",
		Targets:    endpoint.Targets{"v=spf1 include:example.com ~all"},
	})
	if err != nil {
		t.Fatalf("deleteRecord failed: %v", err)
	}

	if len(ms.deleteCalls) != 1 {
		t.Fatalf("Expected 1 delete call, got %d (TXT matching failed with unquoted API content)", len(ms.deleteCalls))
	}
}

// TestFlexBool_MarshalJSON verifies FlexBool serializes back to JSON booleans
func TestFlexBool_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    FlexBool
		expected string
	}{
		{"true serializes to true", FlexBool(true), "true"},
		{"false serializes to false", FlexBool(false), "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("MarshalJSON failed: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("MarshalJSON(%v) = %s, want %s", tt.input, string(data), tt.expected)
			}
		})
	}

	// Round-trip: unmarshal int, marshal back to bool
	var b FlexBool
	if err := json.Unmarshal([]byte("1"), &b); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(data) != "true" {
		t.Errorf("Round-trip: unmarshal 1 then marshal = %s, want true", string(data))
	}
}

// TestFlexBool_RecordStructUnmarshal verifies a full Record JSON with int disabled field deserializes correctly
func TestFlexBool_RecordStructUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected FlexBool
	}{
		{
			"disabled as int 0",
			`{"id":1,"zone_id":1,"name":"test.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":0}`,
			FlexBool(false),
		},
		{
			"disabled as int 1",
			`{"id":2,"zone_id":1,"name":"test.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":1}`,
			FlexBool(true),
		},
		{
			"disabled as bool false",
			`{"id":3,"zone_id":1,"name":"test.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":false}`,
			FlexBool(false),
		},
		{
			"disabled as bool true",
			`{"id":4,"zone_id":1,"name":"test.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":true}`,
			FlexBool(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec Record
			if err := json.Unmarshal([]byte(tt.json), &rec); err != nil {
				t.Fatalf("Failed to unmarshal Record: %v", err)
			}
			if rec.Disabled != tt.expected {
				t.Errorf("Record.Disabled = %v, want %v", rec.Disabled, tt.expected)
			}
		})
	}
}

// TestV2API_ListRecords_DisabledAsInt verifies V2 records with int disabled field are parsed correctly
func TestV2API_ListRecords_DisabledAsInt(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}

	// Create a mock server that returns disabled as int for V2
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/zones", func(w http.ResponseWriter, r *http.Request) {
		resp := ZonesResponseV2{Success: true}
		resp.Data.Zones = zones
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/v2/zones/", func(w http.ResponseWriter, r *http.Request) {
		// Return records with disabled as integer (0/1) via raw JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":[` +
			`{"id":101,"zone_id":1,"name":"www.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":0},` +
			`{"id":102,"zone_id":1,"name":"disabled.example.com","type":"A","content":"2.2.2.2","ttl":300,"disabled":1}` +
			`]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("V2 Records failed with int disabled field: %v", err)
	}

	if len(endpoints) != 2 {
		t.Errorf("Expected 2 endpoints, got %d", len(endpoints))
	}
}
