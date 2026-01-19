package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantKeyFunc(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		query    string
		expected string
	}{
		{"from header", "tenant-123", "", "tenant:tenant-123"},
		{"from query", "", "tenant-456", "tenant:tenant-456"},
		{"header takes precedence", "tenant-123", "tenant-456", "tenant:tenant-123"},
		{"no tenant", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.header != "" {
				req.Header.Set("X-Tenant-ID", tt.header)
			}
			if tt.query != "" {
				q := req.URL.Query()
				q.Set("tenant_id", tt.query)
				req.URL.RawQuery = q.Encode()
			}

			result := TenantKeyFunc(req)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIPKeyFunc(t *testing.T) {
	tests := []struct {
		name       string
		forwarded  string
		realIP     string
		remoteAddr string
		expected   string
	}{
		{"X-Forwarded-For", "1.2.3.4", "", "5.6.7.8:1234", "ip:1.2.3.4"},
		{"X-Real-IP", "", "1.2.3.4", "5.6.7.8:1234", "ip:1.2.3.4"},
		{"RemoteAddr fallback", "", "", "5.6.7.8:1234", "ip:5.6.7.8:1234"},
		{"Forwarded takes precedence", "1.1.1.1", "2.2.2.2", "3.3.3.3:1234", "ip:1.1.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			req.RemoteAddr = tt.remoteAddr

			result := IPKeyFunc(req)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestRateLimitMiddleware_NoLimiter(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RateLimitMiddleware(nil, nil, TenantKeyFunc)
	wrapped := middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}
