package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecordRequest(t *testing.T) {
	RecordRequest("GET", "/test", 200, 100*time.Millisecond)
	RecordRequest("POST", "/test", 201, 50*time.Millisecond)
	RecordRequest("GET", "/test", 404, 10*time.Millisecond)
}

func TestRecordNotificationEnqueued(t *testing.T) {
	RecordNotificationEnqueued("tenant-1", "email")
	RecordNotificationEnqueued("tenant-2", "sms")
}

func TestRecordNotificationProcessed(t *testing.T) {
	RecordNotificationProcessed("delivered", "email")
	RecordNotificationProcessed("failed", "sms")
}

func TestRecordNotificationLatency(t *testing.T) {
	RecordNotificationLatency("email", 500*time.Millisecond)
	RecordNotificationLatency("sms", 200*time.Millisecond)
}

func TestSetSQSMessagesInFlight(t *testing.T) {
	SetSQSMessagesInFlight(10)
	SetSQSMessagesInFlight(5)
	SetSQSMessagesInFlight(0)
}

func TestRecordIdempotencyHit(t *testing.T) {
	RecordIdempotencyHit()
	RecordIdempotencyHit()
}

func TestRecordRateLimitRejection(t *testing.T) {
	RecordRateLimitRejection("tenant-1")
	RecordRateLimitRejection("tenant-2")
}

func TestSetDBConnections(t *testing.T) {
	SetDBConnections(10)
	SetDBConnections(20)
}

func TestSetRedisConnections(t *testing.T) {
	SetRedisConnections(5)
	SetRedisConnections(10)
}

func TestHandler(t *testing.T) {
	handler := Handler()
	if handler == nil {
		t.Error("Handler should not return nil")
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if len(body) == 0 {
		t.Error("metrics response should not be empty")
	}
}

func TestMiddleware(t *testing.T) {
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusCreated)
	})

	handler := Middleware(inner)
	req := httptest.NewRequest("POST", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !innerCalled {
		t.Error("inner handler should have been called")
	}

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
}

func TestResponseWriter_DefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

	rw.Write([]byte("test"))

	if rw.status != http.StatusOK {
		t.Errorf("expected default status 200, got %d", rw.status)
	}
}

func TestResponseWriter_ExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)

	if rw.status != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rw.status)
	}
}
