package poweradmin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

// Note: Test records use full DNS names (e.g., "www.example.com") to match
// the PowerAdmin API response format.

// mockServer creates a test server that tracks API calls
type mockServer struct {
	server          *httptest.Server
	mu              sync.Mutex // guards all fields below across handler goroutines
	zones           []Zone
	records         map[int][]Record // zoneID -> records
	failRecordsList map[int]bool     // zoneID -> respond 500 to record listing
	createCalls     []CreateRecordRequest
	updateCalls     []updateCall
	deleteCalls     []deleteCall
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
	mux.HandleFunc("/api/v2/zones", func(w http.ResponseWriter, _ *http.Request) {
		ms.mu.Lock()
		defer ms.mu.Unlock()
		resp := ZonesResponseV2{ResponseStatus: ResponseStatus{Success: true}}
		resp.Data.Zones = ms.zones
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Zone records - handles all /api/v2/zones/{id}/* paths
	mux.HandleFunc("/api/v2/zones/", func(w http.ResponseWriter, r *http.Request) {
		ms.mu.Lock()
		defer ms.mu.Unlock()
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
			if ms.failRecordsList[zoneID] {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"success":false,"message":"boom"}`))
				return
			}
			if recs, ok := ms.records[zoneID]; ok {
				resp := RecordsResponseV2Records{ResponseStatus: ResponseStatus{Success: true}}
				resp.Data.Records = recs
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				resp := RecordsResponseV2Records{ResponseStatus: ResponseStatus{Success: true}}
				resp.Data.Records = []Record{}
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
			resp := RecordResponseV2{ResponseStatus: ResponseStatus{Success: true}}
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
				resp := RecordResponseV2{ResponseStatus: ResponseStatus{Success: true}}
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
	provider.zoneCache = zones[:1]

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
	provider.zoneCache = zones[:1]

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

// TestRecords_AggregatesMultiTarget verifies records sharing a name and type
// are returned as a single multi-target endpoint. external-dns's plan keeps
// only one current endpoint per (name, type), so separate endpoints per record
// would make multi-target record sets flap forever.
func TestRecords_AggregatesMultiTarget(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "2.2.2.2", TTL: 300},
			{ID: 103, ZoneID: 1, Name: "www.example.com", Type: "AAAA", Content: "::1", TTL: 300},
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
		t.Fatalf("Expected 2 endpoints (A + AAAA), got %d", len(endpoints))
	}

	for _, ep := range endpoints {
		switch ep.RecordType {
		case "A":
			if len(ep.Targets) != 2 {
				t.Errorf("Expected 2 A targets, got %v", ep.Targets)
			}
		case "AAAA":
			if len(ep.Targets) != 1 {
				t.Errorf("Expected 1 AAAA target, got %v", ep.Targets)
			}
		default:
			t.Errorf("Unexpected record type %s", ep.RecordType)
		}
	}
}

// TestUpdateRecord_AddTarget verifies growing a target set creates the surplus
// target and leaves the unchanged record alone.
func TestUpdateRecord_AddTarget(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache = zones[:1]

	oldEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1"},
		RecordTTL:  300,
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1", "2.2.2.2"},
		RecordTTL:  300,
	}

	if err := provider.updateRecord(context.Background(), oldEp, newEp); err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Fatalf("Expected 1 create call for the added target, got %d", len(ms.createCalls))
	}
	if ms.createCalls[0].Content != "2.2.2.2" {
		t.Errorf("Expected create for 2.2.2.2, got %q", ms.createCalls[0].Content)
	}
	if len(ms.updateCalls) != 0 {
		t.Errorf("Expected 0 update calls for unchanged target, got %d", len(ms.updateCalls))
	}
	if len(ms.deleteCalls) != 0 {
		t.Errorf("Expected 0 delete calls, got %d", len(ms.deleteCalls))
	}
}

// TestUpdateRecord_RemoveTarget verifies shrinking a target set deletes the
// orphaned record instead of leaving it to serve stale answers.
func TestUpdateRecord_RemoveTarget(t *testing.T) {
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
	provider.zoneCache = zones[:1]

	oldEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1", "2.2.2.2"},
		RecordTTL:  300,
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1"},
		RecordTTL:  300,
	}

	if err := provider.updateRecord(context.Background(), oldEp, newEp); err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	if len(ms.deleteCalls) != 1 {
		t.Fatalf("Expected 1 delete call for the removed target, got %d", len(ms.deleteCalls))
	}
	if ms.deleteCalls[0].recordID != 102 {
		t.Errorf("Expected record 102 (2.2.2.2) to be deleted, got %d", ms.deleteCalls[0].recordID)
	}
	if len(ms.updateCalls) != 0 {
		t.Errorf("Expected 0 update calls, got %d", len(ms.updateCalls))
	}
	if len(ms.createCalls) != 0 {
		t.Errorf("Expected 0 create calls, got %d", len(ms.createCalls))
	}
}

// TestUpdateRecord_RecreatesDriftedRecord verifies that an old target whose
// record disappeared out-of-band is recreated rather than silently dropped.
func TestUpdateRecord_RecreatesDriftedRecord(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	// Zone is empty: the record external-dns believes exists is gone.
	records := map[int][]Record{1: {}}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache = zones[:1]

	oldEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1"},
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"2.2.2.2"},
		RecordTTL:  300,
	}

	if err := provider.updateRecord(context.Background(), oldEp, newEp); err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Fatalf("Expected 1 create call for the drifted record, got %d", len(ms.createCalls))
	}
	if ms.createCalls[0].Content != "2.2.2.2" {
		t.Errorf("Expected create for 2.2.2.2, got %q", ms.createCalls[0].Content)
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
	content, priority, err := parseTarget("MX", "10 mail.example.com")
	if err != nil {
		t.Fatalf("parseTarget failed: %v", err)
	}

	if content != "mail.example.com" {
		t.Errorf("Expected content 'mail.example.com', got %q", content)
	}
	if priority == nil || *priority != 10 {
		t.Errorf("Expected priority 10, got %v", priority)
	}
}

// TestParseTarget_MXInvalid verifies malformed MX targets error instead of
// silently writing the whole target string as content
func TestParseTarget_MXInvalid(t *testing.T) {
	for _, target := range []string{"mail.example.com", "abc mail.example.com", "10 ", "10"} {
		if _, _, err := parseTarget("MX", target); err == nil {
			t.Errorf("Expected error for MX target %q", target)
		}
	}
}

// TestParseTarget_SRVInvalid verifies incomplete or non-numeric SRV targets
// are rejected instead of writing partial content
func TestParseTarget_SRVInvalid(t *testing.T) {
	for _, target := range []string{"sip.example.com", "10 5 5060", "10 5 5060 ", "10 abc 5060 sip.example.com"} {
		if _, _, err := parseTarget("SRV", target); err == nil {
			t.Errorf("Expected error for SRV target %q", target)
		}
	}
}

// TestParseTarget_SRV verifies the SRV priority is split out for PowerAdmin's
// separate priority field, leaving "weight port target" as content
func TestParseTarget_SRV(t *testing.T) {
	content, priority, err := parseTarget("SRV", "10 5 5060 sip.example.com")
	if err != nil {
		t.Fatalf("parseTarget failed: %v", err)
	}

	if content != "5 5060 sip.example.com" {
		t.Errorf("Expected content '5 5060 sip.example.com', got %q", content)
	}
	if priority == nil || *priority != 10 {
		t.Errorf("Expected priority 10, got %v", priority)
	}
}

func TestParseTarget_TXT(t *testing.T) {
	// Input with quotes should be normalized and re-quoted
	content, priority, err := parseTarget("TXT", "\"v=spf1 include:example.com ~all\"")
	if err != nil {
		t.Fatalf("parseTarget failed: %v", err)
	}
	if content != "\"v=spf1 include:example.com ~all\"" {
		t.Errorf("Expected quoted content, got %q", content)
	}
	if priority != nil {
		t.Error("Expected nil priority for TXT record")
	}

	// Input without quotes should get quotes added
	content2, _, _ := parseTarget("TXT", "v=spf1 include:example.com ~all")
	if content2 != "\"v=spf1 include:example.com ~all\"" {
		t.Errorf("Expected quoted content, got %q", content2)
	}

	// Quotes that are part of the value must survive: only the surrounding
	// pair is stripped before re-quoting
	content3, _, _ := parseTarget("TXT", "\"\"inner\"\"")
	if content3 != "\"\"inner\"\"" {
		t.Errorf("Expected inner quotes preserved, got %q", content3)
	}
}

func TestParseTarget_A(t *testing.T) {
	content, priority, err := parseTarget("A", "192.168.1.1")
	if err != nil {
		t.Fatalf("parseTarget failed: %v", err)
	}

	if content != "192.168.1.1" {
		t.Errorf("Expected content '192.168.1.1', got %q", content)
	}
	if priority != nil {
		t.Error("Expected nil priority for A record")
	}
}

func TestParseTarget_LUA(t *testing.T) {
	// LUA content carries an embedded query type plus a quoted Lua expression and
	// must round-trip verbatim: no quote stripping, no priority parsing.
	target := "A \"ifurlup('https://example.com', {'192.0.2.1','198.51.100.1'})\""
	content, priority, err := parseTarget("LUA", target)
	if err != nil {
		t.Fatalf("parseTarget failed: %v", err)
	}

	if content != target {
		t.Errorf("Expected content unchanged %q, got %q", target, content)
	}
	if priority != nil {
		t.Error("Expected nil priority for LUA record")
	}
}

// TestRecords_SRVWithPriority verifies SRV records round-trip: the priority
// stored separately by PowerAdmin is folded back into the target
func TestRecords_SRVWithPriority(t *testing.T) {
	priority := 10
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "_sip._tcp.example.com", Type: "SRV", Content: "5 5060 sip.example.com", TTL: 300, Priority: &priority},
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

	expected := "10 5 5060 sip.example.com"
	if endpoints[0].Targets[0] != expected {
		t.Errorf("Expected SRV target %q, got %q", expected, endpoints[0].Targets[0])
	}
}

// TestCreateRecord_SRV verifies the SRV priority is sent in the priority field
// rather than embedded in content
func TestCreateRecord_SRV(t *testing.T) {
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
				DNSName:    "_sip._tcp.example.com",
				RecordType: "SRV",
				Targets:    endpoint.Targets{"10 5 5060 sip.example.com"},
				RecordTTL:  300,
			},
		},
	}

	if err := provider.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Fatalf("Expected 1 create call, got %d", len(ms.createCalls))
	}
	if ms.createCalls[0].Content != "5 5060 sip.example.com" {
		t.Errorf("Expected content '5 5060 sip.example.com', got %q", ms.createCalls[0].Content)
	}
	if ms.createCalls[0].Priority == nil || *ms.createCalls[0].Priority != 10 {
		t.Errorf("Expected priority 10, got %v", ms.createCalls[0].Priority)
	}
}

func TestIsSupportedRecordType(t *testing.T) {
	supported := []string{"A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "PTR", "CAA", "LUA"}
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

// TestRecords_FailsOnZoneListError verifies a zone whose records cannot be
// listed fails the whole call instead of returning a partial view, which
// would make external-dns recreate the missing records as duplicates
func TestRecords_FailsOnZoneListError(t *testing.T) {
	zones := []Zone{
		{ID: 1, Name: "example.com"},
		{ID: 2, Name: "broken.org"},
	}

	ms := newMockServer(zones, map[int][]Record{1: {}})
	ms.failRecordsList = map[int]bool{2: true}
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com", "broken.org"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if _, err := provider.Records(context.Background()); err == nil {
		t.Fatal("Expected Records to fail when a zone's records cannot be listed")
	}
}

// TestApplyChanges_NarrowerFilterThanZone verifies a domain filter scoped to a
// subdomain still finds the parent zone that hosts those records
func TestApplyChanges_NarrowerFilterThanZone(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	ms := newMockServer(zones, map[int][]Record{1: {}})
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"app.example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{DNSName: "www.app.example.com", RecordType: "A", Targets: endpoint.Targets{"1.1.1.1"}, RecordTTL: 300},
		},
	}

	if err := provider.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Fatalf("Expected 1 create call, got %d", len(ms.createCalls))
	}
	if ms.createCalls[0].Name != "www.app" {
		t.Errorf("Expected record name www.app, got %q", ms.createCalls[0].Name)
	}
}

// TestRecords_NarrowerFilterThanZone verifies that when a parent zone is
// admitted for a subdomain filter, only records matching the filter are
// exposed as current state
func TestRecords_NarrowerFilterThanZone(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www.app.example.com", Type: "A", Content: "2.2.2.2", TTL: 300},
		},
	}
	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"app.example.com"})
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
	if endpoints[0].DNSName != "www.app.example.com" {
		t.Errorf("Expected only www.app.example.com, got %s", endpoints[0].DNSName)
	}
}

// TestZoneCache_EvictsRemovedZones verifies a zone deleted from PowerAdmin
// disappears from the cache on refresh instead of lingering forever
func TestZoneCache_EvictsRemovedZones(t *testing.T) {
	zones := []Zone{
		{ID: 1, Name: "example.com"},
		{ID: 2, Name: "other.org"},
	}
	ms := newMockServer(zones, map[int][]Record{1: {}, 2: {}})
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com", "other.org"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if _, err := provider.Records(context.Background()); err != nil {
		t.Fatalf("Records failed: %v", err)
	}
	if _, err := provider.findZoneForName("www.other.org"); err != nil {
		t.Fatalf("Expected other.org in cache: %v", err)
	}

	// Zone disappears from PowerAdmin
	ms.mu.Lock()
	ms.zones = zones[:1]
	ms.mu.Unlock()
	if _, err := provider.Records(context.Background()); err != nil {
		t.Fatalf("Records failed: %v", err)
	}
	if _, err := provider.findZoneForName("www.other.org"); err == nil {
		t.Error("Expected removed zone to be evicted from the cache")
	}
}

// TestProvider_ConcurrentAccess exercises Records and ApplyChanges from
// concurrent requests; the race detector flags unsynchronized cache access
func TestProvider_ConcurrentAccess(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	ms := newMockServer(zones, map[int][]Record{1: {}})
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = provider.Records(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = provider.ApplyChanges(context.Background(), &plan.Changes{
				Create: []*endpoint.Endpoint{
					{DNSName: "www.example.com", RecordType: "A", Targets: endpoint.Targets{"1.1.1.1"}, RecordTTL: 300},
				},
			})
		}()
	}
	wg.Wait()
}

// Health reflects PowerAdmin reachability: nil when the API responds, an
// error when it does not.
func TestProviderHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"zones":[]}}`))
	}))

	provider, err := NewProvider(server.URL, "test-key", APIVersionV2, nil, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if err := provider.Health(context.Background()); err != nil {
		t.Errorf("expected healthy provider, got: %v", err)
	}

	server.Close()
	if err := provider.Health(context.Background()); err == nil {
		t.Error("expected error when PowerAdmin is unreachable")
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
	provider.zoneCache = zones

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
		zone, err := provider.findZoneForName(tt.dnsName)
		if err != nil {
			t.Errorf("findZoneForName(%s) error: %v", tt.dnsName, err)
			continue
		}
		if zone.Name != tt.expectedZone {
			t.Errorf("findZoneForName(%s) = %s, want %s", tt.dnsName, zone.Name, tt.expectedZone)
		}
	}
}

// TestFindZoneForEndpoint_NoMatch verifies error when no zone matches,
// including names that merely share a suffix without a label boundary
func TestFindZoneForEndpoint_NoMatch(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider("http://example.com", "api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	provider.zoneCache = zones[:1]

	for _, dnsName := range []string{"www.other.org", "notexample.com", "www.notexample.com"} {
		if _, err := provider.findZoneForName(dnsName); err == nil {
			t.Errorf("Expected error for non-matching domain %s", dnsName)
		}
	}
}

// TestCreateRecord_MixedCaseEndpoint verifies mixed-case endpoint names are
// canonicalized before zone matching and record-name extraction
func TestCreateRecord_MixedCaseEndpoint(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{1: {}}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache = zones[:1]

	ep := &endpoint.Endpoint{
		DNSName:    "WWW.Example.COM",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1"},
		RecordTTL:  300,
	}

	if err := provider.createRecord(context.Background(), ep); err != nil {
		t.Fatalf("createRecord failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Fatalf("Expected 1 create call, got %d", len(ms.createCalls))
	}
	if ms.createCalls[0].Name != "www" {
		t.Errorf("Expected record name www, got %q", ms.createCalls[0].Name)
	}
}

// TestRecordMatchesTarget_HostnameContent verifies hostname-valued content
// matches regardless of case and trailing dots
func TestRecordMatchesTarget_HostnameContent(t *testing.T) {
	priority := 10
	tests := []struct {
		name   string
		record Record
		target string
		want   bool
	}{
		{"CNAME case and dot", Record{Type: "CNAME", Content: "Target.Example.COM."}, "target.example.com", true},
		{"MX case and dot", Record{Type: "MX", Content: "Mail.Example.COM.", Priority: &priority}, "10 mail.example.com", true},
		{"MX priority mismatch", Record{Type: "MX", Content: "mail.example.com", Priority: &priority}, "20 mail.example.com", false},
		{"A exact", Record{Type: "A", Content: "1.1.1.1"}, "1.1.1.1", true},
		{"CNAME different host", Record{Type: "CNAME", Content: "other.example.com"}, "target.example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recordMatchesTarget(tt.record, tt.target); got != tt.want {
				t.Errorf("recordMatchesTarget(%v, %q) = %v, want %v", tt.record, tt.target, got, tt.want)
			}
		})
	}
}

// TestRecords_NormalizesMixedCaseNames verifies zone and record names from the
// API are canonicalized so matching does not depend on case or trailing dots
func TestRecords_NormalizesMixedCaseNames(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "Example.COM"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "WWW.Example.COM", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www.example.com.", Type: "A", Content: "2.2.2.2", TTL: 300},
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

	// Both case variants canonicalize to the same name and aggregate
	if len(endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].DNSName != "www.example.com" {
		t.Errorf("Expected canonical name www.example.com, got %s", endpoints[0].DNSName)
	}
	if len(endpoints[0].Targets) != 2 {
		t.Errorf("Expected 2 targets, got %v", endpoints[0].Targets)
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

// TestRecords_LUA verifies LUA records are surfaced (not filtered) with content preserved
func TestRecords_LUA(t *testing.T) {
	luaContent := "A \"ifurlup('https://example.com', {'192.0.2.1','198.51.100.1'})\""
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "gslb.example.com", Type: "LUA", Content: luaContent, TTL: 300},
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
	if endpoints[0].RecordType != "LUA" {
		t.Errorf("Expected record type LUA, got %q", endpoints[0].RecordType)
	}
	if endpoints[0].Targets[0] != luaContent {
		t.Errorf("Expected LUA target %q, got %q", luaContent, endpoints[0].Targets[0])
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
	mux.HandleFunc("/api/v1/zones", func(w http.ResponseWriter, _ *http.Request) {
		resp := ZonesResponseV1{ResponseStatus: ResponseStatus{Success: true}, Data: ms.zones}
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
				_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
			}

		case http.MethodPost:
			var req CreateRecordRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			ms.createCalls = append(ms.createCalls, req)

			newID := len(ms.records[zoneID]) + 100
			// V1 returns flat structure with record_id
			resp := RecordResponseV1{ResponseStatus: ResponseStatus{Success: true}}
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
				resp := APIResponse{ResponseStatus: ResponseStatus{Success: true, Message: "Record updated successfully"}}
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

// TestCreateRecord_LUA verifies a LUA record is sent with type "LUA", content
// preserved verbatim, and no priority (PowerAdmin requires priority 0/empty for LUA).
func TestCreateRecord_LUA(t *testing.T) {
	luaContent := "A \"ifurlup('https://example.com', {'192.0.2.1','198.51.100.1'})\""
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
				DNSName:    "gslb.example.com",
				RecordType: "LUA",
				Targets:    endpoint.Targets{luaContent},
				RecordTTL:  300,
			},
		},
	}

	if err := provider.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if len(ms.createCalls) != 1 {
		t.Fatalf("Expected 1 create call, got %d", len(ms.createCalls))
	}
	if ms.createCalls[0].Type != "LUA" {
		t.Errorf("Expected type 'LUA', got %q", ms.createCalls[0].Type)
	}
	if ms.createCalls[0].Content != luaContent {
		t.Errorf("Expected content %q, got %q", luaContent, ms.createCalls[0].Content)
	}
	if ms.createCalls[0].Priority != nil {
		t.Errorf("Expected nil priority for LUA, got %v", *ms.createCalls[0].Priority)
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
	provider.zoneCache = zones[:1]

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
		{"string 1", "\"1\"", FlexBool(true), false},
		{"string 0", "\"0\"", FlexBool(false), false},
		{"string true", "\"true\"", FlexBool(true), false},
		{"string false", "\"false\"", FlexBool(false), false},
		{"null", "null", FlexBool(false), false},
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

	// Both TXT records share a name and type, so they aggregate into one endpoint
	if len(endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(endpoints))
	}
	if len(endpoints[0].Targets) != 2 {
		t.Fatalf("Expected 2 targets, got %d", len(endpoints[0].Targets))
	}

	// Quoted TXT content should have quotes stripped
	for _, target := range endpoints[0].Targets {
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

	// The V1 mock returns disabled as int (0/1). This should parse without
	// error, and the disabled record must be filtered out.
	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed (disabled as int should be handled): %v", err)
	}

	if len(endpoints) != 1 {
		t.Errorf("Expected 1 endpoint (disabled record filtered), got %d", len(endpoints))
	}
	if len(endpoints) > 0 && endpoints[0].DNSName != "www.example.com" {
		t.Errorf("Expected the enabled record, got %s", endpoints[0].DNSName)
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
	provider.zoneCache = zones[:1]

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
	provider.zoneCache = zones[:1]

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
	mux.HandleFunc("/api/v2/zones", func(w http.ResponseWriter, _ *http.Request) {
		resp := ZonesResponseV2{ResponseStatus: ResponseStatus{Success: true}}
		resp.Data.Zones = zones
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/v2/zones/", func(w http.ResponseWriter, _ *http.Request) {
		// Return records with disabled as integer (0/1) via raw JSON
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"records":[` +
			`{"id":101,"zone_id":1,"name":"www.example.com","type":"A","content":"1.1.1.1","ttl":300,"disabled":0},` +
			`{"id":102,"zone_id":1,"name":"disabled.example.com","type":"A","content":"2.2.2.2","ttl":300,"disabled":1}` +
			`]}}`))
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

	if len(endpoints) != 1 {
		t.Errorf("Expected 1 endpoint (disabled record filtered), got %d", len(endpoints))
	}
	if len(endpoints) > 0 && endpoints[0].DNSName != "www.example.com" {
		t.Errorf("Expected the enabled record, got %s", endpoints[0].DNSName)
	}
}

// TestApplyChanges_ReEnablesDisabledRecord verifies that creating an endpoint
// whose target already exists as a disabled record re-enables that record
// instead of stacking an enabled duplicate next to it.
func TestApplyChanges_ReEnablesDisabledRecord(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300, Disabled: FlexBool(true)},
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
			{DNSName: "www.example.com", RecordType: "A", Targets: endpoint.Targets{"1.1.1.1"}, RecordTTL: 300},
		},
	}
	if err := provider.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if len(ms.createCalls) != 0 {
		t.Errorf("Expected no create calls (disabled record should be re-enabled), got %d", len(ms.createCalls))
	}
	if len(ms.updateCalls) != 1 {
		t.Fatalf("Expected 1 update call (re-enable), got %d", len(ms.updateCalls))
	}
	if ms.updateCalls[0].recordID != 101 {
		t.Errorf("Expected update of record 101, got %d", ms.updateCalls[0].recordID)
	}
	if ms.updateCalls[0].request.Disabled {
		t.Error("Expected the record to be re-enabled (disabled=false)")
	}
}

// TestUpdateRecord_ReEnablesDisabledRecordForNewTarget verifies that a target
// added by an update re-enables a matching disabled record instead of
// creating a duplicate.
func TestUpdateRecord_ReEnablesDisabledRecordForNewTarget(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "2.2.2.2", TTL: 300, Disabled: FlexBool(true)},
		},
	}

	ms := newMockServer(zones, records)
	defer ms.Close()

	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider(ms.server.URL, "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache = zones[:1]

	oldEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1"},
		RecordTTL:  300,
	}
	newEp := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1", "2.2.2.2"},
		RecordTTL:  300,
	}

	if err := provider.updateRecord(context.Background(), oldEp, newEp); err != nil {
		t.Fatalf("updateRecord failed: %v", err)
	}

	if len(ms.createCalls) != 0 {
		t.Errorf("Expected no create calls, got %d", len(ms.createCalls))
	}
	if len(ms.updateCalls) != 1 {
		t.Fatalf("Expected 1 update call (re-enable of record 102), got %d", len(ms.updateCalls))
	}
	if ms.updateCalls[0].recordID != 102 {
		t.Errorf("Expected update of record 102, got %d", ms.updateCalls[0].recordID)
	}
	if ms.updateCalls[0].request.Disabled {
		t.Error("Expected the record to be re-enabled (disabled=false)")
	}
}

// TestDeleteRecord_SkipsDisabledDuplicate verifies that deletes only claim
// enabled records: a disabled record with the same content is invisible to
// external-dns and must not be consumed by the target multiset.
func TestDeleteRecord_SkipsDisabledDuplicate(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300, Disabled: FlexBool(true)},
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
	provider.zoneCache = zones[:1]

	ep := &endpoint.Endpoint{
		DNSName:    "www.example.com",
		RecordType: "A",
		Targets:    endpoint.Targets{"1.1.1.1"},
	}
	if err := provider.deleteRecord(context.Background(), ep); err != nil {
		t.Fatalf("deleteRecord failed: %v", err)
	}

	if len(ms.deleteCalls) != 1 {
		t.Fatalf("Expected 1 delete call, got %d", len(ms.deleteCalls))
	}
	if ms.deleteCalls[0].recordID != 102 {
		t.Errorf("Expected deletion of enabled record 102, got %d", ms.deleteCalls[0].recordID)
	}
}

// TestAdjustEndpoints_FiltersUnmanageableEndpoints verifies unsupported record
// types and apex NS endpoints are dropped, and CNAME endpoints are trimmed to
// a single target. Records hides all of these, so leaving them desired would
// recreate them on every reconcile loop.
func TestAdjustEndpoints_FiltersUnmanageableEndpoints(t *testing.T) {
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewProvider("http://localhost", "test-api-key", APIVersionV2, domainFilter, false)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	provider.zoneCache = []Zone{{ID: 1, Name: "example.com"}}

	endpoints := []*endpoint.Endpoint{
		{DNSName: "www.example.com", RecordType: "A", Targets: endpoint.Targets{"1.1.1.1"}},
		{DNSName: "naptr.example.com", RecordType: "NAPTR", Targets: endpoint.Targets{"100 10 \"S\" \"SIP+D2U\" \"\" _sip._udp.example.com."}},
		{DNSName: "example.com", RecordType: "NS", Targets: endpoint.Targets{"ns1.example.com"}},
		{DNSName: "sub.example.com", RecordType: "NS", Targets: endpoint.Targets{"ns1.example.com"}},
		{DNSName: "alias.example.com", RecordType: "CNAME", Targets: endpoint.Targets{"a.example.com", "b.example.com"}},
	}

	adjusted, err := provider.AdjustEndpoints(endpoints)
	if err != nil {
		t.Fatalf("AdjustEndpoints failed: %v", err)
	}

	byName := make(map[string]*endpoint.Endpoint)
	for _, ep := range adjusted {
		byName[ep.DNSName+"/"+ep.RecordType] = ep
	}

	if len(adjusted) != 3 {
		t.Errorf("Expected 3 endpoints after filtering, got %d", len(adjusted))
	}
	if _, ok := byName["www.example.com/A"]; !ok {
		t.Error("Expected the A endpoint to be kept")
	}
	if _, ok := byName["naptr.example.com/NAPTR"]; ok {
		t.Error("Expected the unsupported NAPTR endpoint to be dropped")
	}
	if _, ok := byName["example.com/NS"]; ok {
		t.Error("Expected the apex NS endpoint to be dropped")
	}
	if _, ok := byName["sub.example.com/NS"]; !ok {
		t.Error("Expected the non-apex NS endpoint to be kept")
	}
	cname, ok := byName["alias.example.com/CNAME"]
	if !ok {
		t.Fatal("Expected the CNAME endpoint to be kept")
	}
	if len(cname.Targets) != 1 || cname.Targets[0] != "a.example.com" {
		t.Errorf("Expected CNAME trimmed to first target, got %v", cname.Targets)
	}
}

// TestRecords_MixedTTLsReportUnconfigured verifies that a record set whose
// members carry different TTLs is exposed with an unconfigured TTL, so the
// plan always sees a TTL difference from the desired value and repairs the
// drifted records.
func TestRecords_MixedTTLsReportUnconfigured(t *testing.T) {
	zones := []Zone{{ID: 1, Name: "example.com"}}
	records := map[int][]Record{
		1: {
			{ID: 101, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "1.1.1.1", TTL: 300},
			{ID: 102, ZoneID: 1, Name: "www.example.com", Type: "A", Content: "2.2.2.2", TTL: 600},
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
		t.Fatalf("Expected 1 aggregated endpoint, got %d", len(endpoints))
	}
	if len(endpoints[0].Targets) != 2 {
		t.Errorf("Expected 2 targets, got %v", endpoints[0].Targets)
	}
	if endpoints[0].RecordTTL.IsConfigured() {
		t.Errorf("Expected unconfigured TTL for mixed-TTL record set, got %d", endpoints[0].RecordTTL)
	}
}

// TestRecords_UniformTTLsKeepTTL verifies the mixed-TTL handling does not
// disturb record sets whose TTLs agree.
func TestRecords_UniformTTLsKeepTTL(t *testing.T) {
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

	endpoints, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records failed: %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("Expected 1 aggregated endpoint, got %d", len(endpoints))
	}
	if int(endpoints[0].RecordTTL) != 300 {
		t.Errorf("Expected TTL 300, got %d", endpoints[0].RecordTTL)
	}
}
