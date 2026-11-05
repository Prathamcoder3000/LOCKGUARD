package middleware_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/middleware"
)

func TestMiddleware_SecurityHeaders(t *testing.T) {
	handler := middleware.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	headers := rec.Header()
	if headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing or incorrect X-Content-Type-Options: %s", headers.Get("X-Content-Type-Options"))
	}
	if headers.Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing or incorrect X-Frame-Options: %s", headers.Get("X-Frame-Options"))
	}
	if headers.Get("Strict-Transport-Security") == "" {
		t.Errorf("missing Strict-Transport-Security header")
	}
	if headers.Get("Content-Security-Policy") == "" {
		t.Errorf("missing Content-Security-Policy header")
	}
}

func TestMiddleware_MaxBodySize(t *testing.T) {
	// Limit to 64 bytes
	handler := middleware.MaxBodySize(64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Payload with 128 bytes (exceeds limit)
	oversized := bytes.Repeat([]byte("A"), 128)
	req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(oversized))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 RequestEntityTooLarge, got %d", rec.Code)
	}
}

func TestMiddleware_ValidateContentType(t *testing.T) {
	handler := middleware.ValidateContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 1. POST with application/json -> PASS (200 OK)
	reqJSON := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", strings.NewReader(`{"resource_name":"res1"}`))
	reqJSON.Header.Set("Content-Type", "application/json")
	recJSON := httptest.NewRecorder()
	handler.ServeHTTP(recJSON, reqJSON)
	if recJSON.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for application/json, got %d", recJSON.Code)
	}

	// 2. POST with text/plain -> REJECT (415 Unsupported Media Type)
	reqText := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", strings.NewReader(`{"resource_name":"res1"}`))
	reqText.Header.Set("Content-Type", "text/plain")
	recText := httptest.NewRecorder()
	handler.ServeHTTP(recText, reqText)
	if recText.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415 UnsupportedMediaType, got %d", recText.Code)
	}

	// 3. GET request without Content-Type -> PASS (200 OK)
	reqGET := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
	recGET := httptest.NewRecorder()
	handler.ServeHTTP(recGET, reqGET)
	if recGET.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for GET without content-type, got %d", recGET.Code)
	}
}

func TestMiddleware_RateLimiter(t *testing.T) {
	// 5 requests per second, burst 5
	limiter := middleware.NewRateLimiter(5.0, 5.0, 100, metrics.DefaultRegistry)
	handler := middleware.RateLimitMiddleware(limiter, "test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 5 requests must succeed
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d failed unexpectedly: %d", i, rec.Code)
		}
	}

	// 6th request must be rejected with 429 Too Many Requests
	reqBlocked := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", nil)
	reqBlocked.RemoteAddr = "192.168.1.100:12345"
	recBlocked := httptest.NewRecorder()
	handler.ServeHTTP(recBlocked, reqBlocked)

	if recBlocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 TooManyRequests on 6th request, got %d", recBlocked.Code)
	}
	if recBlocked.Header().Get("Retry-After") == "" {
		t.Fatalf("missing Retry-After header on 429 response")
	}
}

