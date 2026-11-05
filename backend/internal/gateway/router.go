package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/metrics"
)

// MaxRequestBodyBytes limits buffered request payload size to 1 MB.
const MaxRequestBodyBytes = 1 << 20

type errorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type errorEnvelope struct {
	Success   bool        `json:"success"`
	Error     errorDetail `json:"error"`
	Timestamp string      `json:"timestamp"`
}

func respondError(w http.ResponseWriter, status int, code, message string, details interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Success: false,
		Error: errorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// Router acts as the reverse-proxy gateway routing lock operations to the active Raft leader.
type Router struct {
	discovery      *LeaderDiscovery
	httpClient     *http.Client
	maxRedirects   int
	requestTimeout time.Duration
	metrics        *metrics.Registry
}

// NewRouter constructs a new reverse-proxy Router.
func NewRouter(cfg *config.Config, discovery *LeaderDiscovery, reg *metrics.Registry) *Router {
	if reg == nil {
		reg = metrics.DefaultRegistry
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	timeout := cfg.Gateway.RequestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return &Router{
		discovery:      discovery,
		httpClient:     httpClient,
		maxRedirects:   cfg.Gateway.MaxRedirectAttempts,
		requestTimeout: timeout,
		metrics:        reg,
	}
}

// ProxyRequest proxies an incoming HTTP request to the discovered Raft leader.
func (rt *Router) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	normPath := normalizeRoutePath(r.URL.Path)

	// Enforce bounded request body buffer (max 1 MB) to avoid memory exhaustion
	var bodyBytes []byte
	if r.Body != nil {
		limitedReader := http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)
		var err error
		bodyBytes, err = io.ReadAll(limitedReader)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
					"status": strconv.Itoa(http.StatusRequestEntityTooLarge),
					"method": r.Method,
					"path":   normPath,
				})
				respondError(w, http.StatusRequestEntityTooLarge, "ERR_PAYLOAD_TOO_LARGE",
					fmt.Sprintf("request body exceeds maximum allowed size of %d bytes", MaxRequestBodyBytes), nil)
				return
			}
			respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "failed to read request body", nil)
			return
		}
		_ = r.Body.Close()
	}

	// Ensure Request ID is present and preserved across upstream retries
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = generateRequestID()
	}

	ctx := r.Context()
	attempts := 0
	maxAttempts := rt.maxRedirects + 1

	for attempts < maxAttempts {
		attempts++

		// Check for client context cancellation before next attempt
		select {
		case <-ctx.Done():
			rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
				"status": "499",
				"method": r.Method,
				"path":   normPath,
			})
			respondError(w, 499, "ERR_CLIENT_CLOSED_REQUEST", "client cancelled request", nil)
			return
		default:
		}

		leaderBaseURL, err := rt.discovery.GetLeader(ctx)
		if err != nil {
			rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
				"status": strconv.Itoa(http.StatusServiceUnavailable),
				"method": r.Method,
				"path":   normPath,
			})
			respondError(w, http.StatusServiceUnavailable, "ERR_QUORUM_UNAVAILABLE",
				fmt.Sprintf("cluster quorum unavailable or leader unreachable: %v", err), nil)
			return
		}

		targetURL, err := url.Parse(leaderBaseURL + r.URL.RequestURI())
		if err != nil {
			respondError(w, http.StatusInternalServerError, "ERR_INTERNAL", "failed to parse upstream url", nil)
			return
		}

		// Prepare outbound request with buffered body and preserved Request ID
		outReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			respondError(w, http.StatusInternalServerError, "ERR_INTERNAL", "failed to create upstream request", nil)
			return
		}

		copyHeaders(r.Header, outReq.Header)
		stripHopByHopHeaders(outReq.Header)
		outReq.Header.Set("X-Request-ID", requestID)
		outReq.Header.Set("X-Forwarded-For", r.RemoteAddr)

		resp, err := rt.httpClient.Do(outReq)
		if err != nil {
			// Leader connection failed or timed out — invalidate cache and retry next endpoint
			rt.discovery.InvalidateLeader()
			if attempts < maxAttempts {
				select {
				case <-ctx.Done():
					rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
						"status": "499",
						"method": r.Method,
						"path":   normPath,
					})
					respondError(w, 499, "ERR_CLIENT_CLOSED_REQUEST", "client cancelled request", nil)
					return
				case <-time.After(20 * time.Millisecond):
				}
				continue
			}
			rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
				"status": strconv.Itoa(http.StatusServiceUnavailable),
				"method": r.Method,
				"path":   normPath,
			})
			respondError(w, http.StatusServiceUnavailable, "ERR_QUORUM_UNAVAILABLE",
				"coordinator leader request failed: "+err.Error(), nil)
			return
		}

		// Check for Follower Misdirected Request (HTTP 421)
		if resp.StatusCode == http.StatusMisdirectedRequest {
			leaderID := resp.Header.Get("X-Raft-Leader-ID")
			leaderAddr := resp.Header.Get("X-Raft-Leader-Address")
			_ = resp.Body.Close()

			if leaderID != "" {
				rt.discovery.SetLeaderByID(leaderID)
			} else {
				rt.discovery.InvalidateLeader()
			}

			if attempts < maxAttempts {
				select {
				case <-ctx.Done():
					rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
						"status": "499",
						"method": r.Method,
						"path":   normPath,
					})
					respondError(w, 499, "ERR_CLIENT_CLOSED_REQUEST", "client cancelled request", nil)
					return
				case <-time.After(20 * time.Millisecond):
				}
				continue
			}

			// Exceeded max redirect attempts, forward 421
			rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
				"status": strconv.Itoa(http.StatusMisdirectedRequest),
				"method": r.Method,
				"path":   normPath,
			})
			respondError(w, http.StatusMisdirectedRequest, "ERR_NOT_LEADER",
				"exceeded maximum redirection attempts resolving raft leader", map[string]interface{}{
					"leader_id":      leaderID,
					"leader_address": leaderAddr,
				})
			return
		}

		// Forward response to client
		defer resp.Body.Close()
		copyHeaders(resp.Header, w.Header())
		stripHopByHopHeaders(w.Header())
		w.Header().Set("X-Request-ID", requestID)
		w.WriteHeader(resp.StatusCode)

		_, _ = io.Copy(w, resp.Body)

		rt.metrics.IncCounter("lockguard_gateway_requests_total", map[string]string{
			"status": strconv.Itoa(resp.StatusCode),
			"method": r.Method,
			"path":   normPath,
		})
		return
	}

	respondError(w, http.StatusServiceUnavailable, "ERR_QUORUM_UNAVAILABLE",
		"cluster quorum unavailable: could not contact leader", nil)
}

// normalizeRoutePath normalizes dynamic path segments to prevent Prometheus cardinality explosion.
func normalizeRoutePath(p string) string {
	if strings.HasPrefix(p, "/v1/locks/") {
		return "/v1/locks/:resource"
	}
	if strings.HasPrefix(p, "/v1/clients/") {
		return "/v1/clients/:id"
	}
	return p
}

func copyHeaders(src, dst http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

func stripHopByHopHeaders(header http.Header) {
	for _, h := range hopByHopHeaders {
		header.Del(h)
	}
}

func generateRequestID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
