package poweradmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIVersion represents the PowerAdmin API version
type APIVersion string

const (
	// APIVersionV1 is the v1 API version
	APIVersionV1 APIVersion = "v1"
	// APIVersionV2 is the v2 API version (default)
	APIVersionV2 APIVersion = "v2"
)

// Client is a PowerAdmin API client
type Client struct {
	baseURL    string
	apiKey     string
	apiVersion APIVersion
	httpClient *http.Client
}

// NewClient creates a new PowerAdmin API client with the specified API version
func NewClient(baseURL, apiKey string, apiVersion APIVersion) *Client {
	if apiVersion == "" {
		apiVersion = APIVersionV2
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		apiVersion: apiVersion,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Zone represents a DNS zone in PowerAdmin
type Zone struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Record represents a DNS record in PowerAdmin
type Record struct {
	ID       int    `json:"id"`
	ZoneID   int    `json:"zone_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
	Disabled bool   `json:"disabled"`
}

// CreateRecordRequest represents the request body for creating a record
type CreateRecordRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
	Disabled bool   `json:"disabled"`
}

// UpdateRecordRequest represents the request body for updating a record
type UpdateRecordRequest struct {
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Content  string `json:"content,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
	Priority *int   `json:"priority,omitempty"`
	Disabled bool   `json:"disabled"`
}

// APIResponse represents the standard PowerAdmin API response
type APIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// ZonesResponseV2 represents the response from the zones list endpoint (V2 API)
type ZonesResponseV2 struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Zones []Zone `json:"zones"`
	} `json:"data"`
}

// ZonesResponseV1 represents the response from the zones list endpoint (V1 API)
type ZonesResponseV1 struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    []Zone `json:"data"`
}

// RecordsResponse represents the response from the records list endpoint (same for V1 and V2)
type RecordsResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Data    []Record `json:"data"`
}

// RecordResponseV2 represents the response from a single record operation (V2 API)
type RecordResponseV2 struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Record Record `json:"record"`
	} `json:"data"`
}

// RecordResponseV1 represents the response from a single record operation (V1 API)
type RecordResponseV1 struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		RecordID int    `json:"record_id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Content  string `json:"content"`
		TTL      int    `json:"ttl"`
		Priority *int   `json:"priority,omitempty"`
		Disabled int    `json:"disabled"`
	} `json:"data"`
}

// doRequest performs an HTTP request to the PowerAdmin API
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	url := fmt.Sprintf("%s/api/%s%s", c.baseURL, c.apiVersion, path)
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ListZones returns all zones from PowerAdmin
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/zones", nil)
	if err != nil {
		return nil, err
	}

	if c.apiVersion == APIVersionV1 {
		var response ZonesResponseV1
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to unmarshal zones response: %w", err)
		}
		if !response.Success {
			return nil, fmt.Errorf("API returned error: %s", response.Message)
		}
		return response.Data, nil
	}

	// V2 API
	var response ZonesResponseV2
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal zones response: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("API returned error: %s", response.Message)
	}

	return response.Data.Zones, nil
}

// ListRecords returns all records for a specific zone
func (c *Client) ListRecords(ctx context.Context, zoneID int) ([]Record, error) {
	path := fmt.Sprintf("/zones/%d/records", zoneID)
	respBody, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var response RecordsResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal records response: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("API returned error: %s", response.Message)
	}

	return response.Data, nil
}

// CreateRecord creates a new DNS record in the specified zone
func (c *Client) CreateRecord(ctx context.Context, zoneID int, record CreateRecordRequest) (*Record, error) {
	path := fmt.Sprintf("/zones/%d/records", zoneID)
	respBody, err := c.doRequest(ctx, http.MethodPost, path, record)
	if err != nil {
		return nil, err
	}

	if c.apiVersion == APIVersionV1 {
		var response RecordResponseV1
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to unmarshal create record response: %w", err)
		}
		if !response.Success {
			return nil, fmt.Errorf("API returned error: %s", response.Message)
		}
		// Convert V1 response to Record
		disabled := response.Data.Disabled != 0
		return &Record{
			ID:       response.Data.RecordID,
			ZoneID:   zoneID,
			Name:     response.Data.Name,
			Type:     response.Data.Type,
			Content:  response.Data.Content,
			TTL:      response.Data.TTL,
			Priority: response.Data.Priority,
			Disabled: disabled,
		}, nil
	}

	// V2 API
	var response RecordResponseV2
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal create record response: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("API returned error: %s", response.Message)
	}

	return &response.Data.Record, nil
}

// UpdateRecord updates an existing DNS record
func (c *Client) UpdateRecord(ctx context.Context, zoneID, recordID int, record UpdateRecordRequest) (*Record, error) {
	path := fmt.Sprintf("/zones/%d/records/%d", zoneID, recordID)
	respBody, err := c.doRequest(ctx, http.MethodPut, path, record)
	if err != nil {
		return nil, err
	}

	if c.apiVersion == APIVersionV1 {
		// V1 API returns null data on update success
		var response APIResponse
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to unmarshal update record response: %w", err)
		}
		if !response.Success {
			return nil, fmt.Errorf("API returned error: %s", response.Message)
		}
		// V1 doesn't return the updated record, construct from request
		disabled := record.Disabled
		return &Record{
			ID:       recordID,
			ZoneID:   zoneID,
			Name:     record.Name,
			Type:     record.Type,
			Content:  record.Content,
			TTL:      record.TTL,
			Priority: record.Priority,
			Disabled: disabled,
		}, nil
	}

	// V2 API
	var response RecordResponseV2
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal update record response: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("API returned error: %s", response.Message)
	}

	return &response.Data.Record, nil
}

// DeleteRecord deletes a DNS record
func (c *Client) DeleteRecord(ctx context.Context, zoneID, recordID int) error {
	path := fmt.Sprintf("/zones/%d/records/%d", zoneID, recordID)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	return err
}
