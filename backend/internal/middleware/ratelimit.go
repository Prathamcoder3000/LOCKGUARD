package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"lockguard/backend/internal/auth"
	"lockguard/backend/internal/metrics"
)

type clientBucket struct {
	tokens     float64
	lastUpdate time.Time
}

// RateLimiter implements an in-memory token-bucket rate limiter with bounded memory tracking.
type RateLimiter struct {
	mu           sync.Mutex
	rate         float64 // tokens per second
	capacity     float64 // maximum bucket burst size
	clients      map[string]*clientBucket
	maxClients   int
	metrics      *metrics.Registry
	lastCleanup  time.Time
	cleanupEvery time.Duration
	hmacKey      []byte
}

// NewRateLimiter creates a RateLimiter with specified token rate, burst capacity, and max tracked clients.
// It initializes a cryptographically secure 32-byte key for HMAC-SHA256 token fingerprinting.
func NewRateLimiter(rate, capacity float64, maxClients int, reg *metrics.Registry) *RateLimiter {
	return NewRateLimiterWithKey(rate, capacity, maxClients, reg, nil)
}

// NewRateLimiterWithKey allows passing an explicit HMAC secret key (e.g. for testing or cluster coordination).
func NewRateLimiterWithKey(rate, capacity float64, maxClients int, reg *metrics.Registry, key []byte) *RateLimiter {
	if maxClients <= 0 {
		maxClients = 10000
	}
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			// Fallback deterministic salt if system entropy pool is temporarily unavailable
			key = []byte("lockguard-default-ratelimit-hmac-salt")
		}
	}
	return &RateLimiter{
		rate:         rate,
		capacity:     capacity,
		clients:      make(map[string]*clientBucket),
		maxClients:   maxClients,
		metrics:      reg,
		lastCleanup:  time.Now(),
		cleanupEvery: 1 * time.Minute,
		hmacKey:      key,
	}
}

// Allow reports whether a request from the given client key is permitted under the rate limit.
func (rl *RateLimiter) Allow(clientKey string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Periodic cleanup of stale clients to prevent unbounded memory growth
	if now.Sub(rl.lastCleanup) > rl.cleanupEvery {
		rl.cleanupStaleLocked(now)
		rl.lastCleanup = now
	}

	bucket, exists := rl.clients[clientKey]
	if !exists {
		// Enforce capacity bounds: if map reaches maxClients, evict stale clients
		if len(rl.clients) >= rl.maxClients {
			rl.cleanupStaleLocked(now)
			// If still at capacity, evict the oldest entry by lastUpdate (bounded memory guarantee)
			if len(rl.clients) >= rl.maxClients {
				var oldestKey string
				var oldestTime time.Time
				first := true
				for k, b := range rl.clients {
					if first || b.lastUpdate.Before(oldestTime) {
						oldestKey = k
						oldestTime = b.lastUpdate
						first = false
					}
				}
				if oldestKey != "" {
					delete(rl.clients, oldestKey)
				} else {
					// Safety: return false to prevent memory exhaustion
					return false, time.Second
				}
			}
		}
		bucket = &clientBucket{
			tokens:     rl.capacity - 1, // deduct 1 for the current request
			lastUpdate: now,
		}
		rl.clients[clientKey] = bucket
		return true, 0
	}

	// Refill tokens based on elapsed duration
	elapsed := now.Sub(bucket.lastUpdate).Seconds()
	bucket.tokens += elapsed * rl.rate
	if bucket.tokens > rl.capacity {
		bucket.tokens = rl.capacity
	}
	bucket.lastUpdate = now

	if bucket.tokens >= 1.0 {
		bucket.tokens -= 1.0
		return true, 0
	}

	// Calculate wait time until 1 token is available
	missing := 1.0 - bucket.tokens
	retryAfter := time.Duration((missing / rl.rate) * float64(time.Second))
	if retryAfter < 100*time.Millisecond {
		retryAfter = 100 * time.Millisecond
	}

	return false, retryAfter
}

func (rl *RateLimiter) cleanupStaleLocked(now time.Time) {
	staleThreshold := 5 * time.Minute
	for key, b := range rl.clients {
		if now.Sub(b.lastUpdate) > staleThreshold {
			delete(rl.clients, key)
		}
	}
}

// ClientKeys returns a copy of all tracked client keys currently held in memory.
// Used for testing, diagnostic audits, and verifying credential non-retention.
func (rl *RateLimiter) ClientKeys() []string {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	keys := make([]string, 0, len(rl.clients))
	for k := range rl.clients {
		keys = append(keys, k)
	}
	return keys
}

// TrackedClients returns the count of currently tracked client keys.
func (rl *RateLimiter) TrackedClients() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.clients)
}

// ExtractClientKey derives a safe, bounded rate-limiter identifier from the request:
// 1. If verified auth claims exist in request context, returns "sub:<subject>".
// 2. If Authorization header is provided, returns "token_hmac:<hmac-sha256-hex>" (never raw token).
// 3. Otherwise returns "ip:<remote-host>" from direct network socket (r.RemoteAddr).
// Arbitrary X-Forwarded-For or other user-controlled headers are strictly ignored.
func (rl *RateLimiter) ExtractClientKey(r *http.Request) string {
	// 1. Prefer validated authenticated subject identity from request context
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims != nil {
		sub := strings.TrimSpace(claims.Subject)
		if sub != "" {
			return "sub:" + sub
		}
	}

	// 2. Cryptographic keyed fingerprint of Bearer credential (never raw token storage)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token != "" {
			mac := hmac.New(sha256.New, rl.hmacKey)
			mac.Write([]byte(token))
			fingerprint := hex.EncodeToString(mac.Sum(nil))
			return "token_hmac:" + fingerprint
		}
	}

	// 3. Fallback to direct network socket IP (RemoteAddr); ignore spoofable XFF headers
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return "ip:" + host
	}

	return "ip:" + r.RemoteAddr
}

var fallbackHMACKey = []byte("lockguard-ratelimit-keyed-fallback-salt")

// extractClientKey provides standalone key extraction using the fallback key if no RateLimiter instance is provided.
func extractClientKey(r *http.Request) string {
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims != nil {
		sub := strings.TrimSpace(claims.Subject)
		if sub != "" {
			return "sub:" + sub
		}
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token != "" {
			mac := hmac.New(sha256.New, fallbackHMACKey)
			mac.Write([]byte(token))
			return "token_hmac:" + hex.EncodeToString(mac.Sum(nil))
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return "ip:" + host
	}

	return "ip:" + r.RemoteAddr
}

// RateLimitMiddleware returns an HTTP middleware enforcing rate limits based on client IP or authenticated identity.
func RateLimitMiddleware(limiter *RateLimiter, endpointCategory string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract client key (prefer verified claims, keyed fingerprint, or direct RemoteAddr)
			clientKey := limiter.ExtractClientKey(r)

			allowed, retryAfter := limiter.Allow(clientKey)
			if !allowed {
				if limiter.metrics != nil {
					limiter.metrics.IncCounter("lockguard_rate_limit_exceeded_total", map[string]string{
						"endpoint": endpointCategory,
					})
				}

				retrySeconds := int(retryAfter.Seconds())
				if retrySeconds < 1 {
					retrySeconds = 1
				}

				w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error": map[string]interface{}{
						"code":        "ERR_RATE_LIMITED",
						"message":     "too many requests: rate limit exceeded, please retry later",
						"retry_after": retrySeconds,
					},
					"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
