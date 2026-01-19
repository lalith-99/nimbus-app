package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/redis"
)

// RateLimitMiddleware creates an HTTP middleware that enforces rate limits.
// The keyFunc extracts the rate limit key from the request (e.g., tenant ID, IP).
func RateLimitMiddleware(limiter *redis.RateLimiter, logger *zap.Logger, keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			key := keyFunc(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			result, err := limiter.Allow(r.Context(), key)
			if err != nil {
				logger.Warn("rate limit check failed", zap.Error(err))
				next.ServeHTTP(w, r)
				return
			}

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Remaining+1))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))

			if !result.Allowed {
				retryAfter := time.Until(result.ResetAt).Seconds()
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter)))
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(ErrorResponse{
					Type:   "rate_limit_exceeded",
					Title:  "Too Many Requests",
					Status: http.StatusTooManyRequests,
					Detail: "Rate limit exceeded. Please retry after the specified time.",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// TenantKeyFunc extracts tenant ID from the X-Tenant-ID header or query param.
func TenantKeyFunc(r *http.Request) string {
	if tenantID := r.Header.Get("X-Tenant-ID"); tenantID != "" {
		return "tenant:" + tenantID
	}
	if tenantID := r.URL.Query().Get("tenant_id"); tenantID != "" {
		return "tenant:" + tenantID
	}
	return ""
}

// IPKeyFunc extracts the client IP for rate limiting.
func IPKeyFunc(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return "ip:" + ip
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return "ip:" + ip
	}
	return "ip:" + r.RemoteAddr
}
