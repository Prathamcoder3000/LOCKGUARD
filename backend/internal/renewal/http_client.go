package renewal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPLockClient implements LockClient via HTTP against a gateway or coordinator.
type HTTPLockClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPLockClient creates a new HTTP-based LockClient.
func NewHTTPLockClient(baseURL string, client *http.Client) *HTTPLockClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPLockClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: client,
	}
}

// RenewLock issues a POST /v1/locks/renew request.
func (c *HTTPLockClient) RenewLock(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal renew request: %w", err)
	}

	url := c.baseURL + "/v1/locks/renew"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.RequestID != "" {
		httpReq.Header.Set("X-Request-ID", req.RequestID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Coordinator returns standard JSON envelope: {"success": true, "data": LockResult} or {"success": false, "error": ...}
	var generic map[string]interface{}
	if err := json.Unmarshal(respBytes, &generic); err != nil {
		return nil, fmt.Errorf("failed to parse json response: %w", err)
	}

	success, _ := generic["success"].(bool)
	renewResp := &RenewLockResponse{Success: success}

	if success {
		dataBytes, _ := json.Marshal(generic["data"])
		var result struct {
			Success            bool        `json:"success"`
			Code               string      `json:"code"`
			Message            string      `json:"message"`
			ResourceName       string      `json:"resource_name"`
			OwnerID            string      `json:"owner_id"`
			FencingToken       uint64      `json:"fencing_token"`
			LeaseVersion       uint64      `json:"lease_version"`
			ExpiresAtUnixNano  int64       `json:"expires_at_unix_nano"`
			AcquiredAtUnixNano int64       `json:"acquired_at_unix_nano"`
			TTLNanoseconds     int64       `json:"ttl_nanoseconds"`
			Record             *LockRecord `json:"record"`
		}
		if err := json.Unmarshal(dataBytes, &result); err == nil {
			renewResp.Code = result.Code
			renewResp.Message = result.Message
			if result.Record != nil {
				renewResp.Record = result.Record
			} else {
				renewResp.Record = &LockRecord{
					ResourceName:       result.ResourceName,
					OwnerID:            result.OwnerID,
					FencingToken:       result.FencingToken,
					LeaseVersion:       result.LeaseVersion,
					ExpiresAtUnixNano:  result.ExpiresAtUnixNano,
					AcquiredAtUnixNano: result.AcquiredAtUnixNano,
					TTLNanoseconds:     result.TTLNanoseconds,
				}
			}
		}
		return renewResp, nil
	}

	// Handle error envelope
	if errObj, ok := generic["error"].(map[string]interface{}); ok {
		if code, ok := errObj["code"].(string); ok {
			renewResp.Code = code
		}
		if msg, ok := errObj["message"].(string); ok {
			renewResp.Message = msg
		}
	}
	return renewResp, nil
}

// GetLock queries GET /v1/locks/{resource}.
func (c *HTTPLockClient) GetLock(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
	url := c.baseURL + "/v1/locks/" + resourceName
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}

	var generic map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&generic); err != nil {
		return nil, false, err
	}

	success, _ := generic["success"].(bool)
	if !success {
		return nil, false, nil
	}

	dataBytes, _ := json.Marshal(generic["data"])
	var rec LockRecord
	if err := json.Unmarshal(dataBytes, &rec); err != nil {
		return nil, false, err
	}

	return &rec, true, nil
}
