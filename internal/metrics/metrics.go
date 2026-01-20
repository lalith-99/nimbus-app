package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nimbus_http_requests_total",
			Help: "Total HTTP requests by method, path, and status",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nimbus_http_request_duration_seconds",
			Help:    "HTTP request latency distribution",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"method", "path"},
	)

	notificationsEnqueued = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nimbus_notifications_enqueued_total",
			Help: "Total notifications enqueued by tenant and channel",
		},
		[]string{"tenant_id", "channel"},
	)

	notificationsProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nimbus_notifications_processed_total",
			Help: "Total notifications processed by status",
		},
		[]string{"status", "channel"},
	)

	notificationLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nimbus_notification_latency_seconds",
			Help:    "Time from enqueue to delivery",
			Buckets: []float64{.1, .5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"channel"},
	)

	sqsMessagesInFlight = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "nimbus_sqs_messages_in_flight",
			Help: "Current messages being processed from SQS",
		},
	)

	idempotencyHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "nimbus_idempotency_hits_total",
			Help: "Requests served from idempotency cache",
		},
	)

	rateLimitRejections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nimbus_rate_limit_rejections_total",
			Help: "Requests rejected by rate limiter",
		},
		[]string{"tenant_id"},
	)

	dbConnectionsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "nimbus_db_connections_active",
			Help: "Active database connections",
		},
	)

	redisConnectionsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "nimbus_redis_connections_active",
			Help: "Active Redis connections",
		},
	)
)

// Handler returns the Prometheus metrics HTTP handler
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordRequest records HTTP request metrics
func RecordRequest(method, path string, status int, duration time.Duration) {
	httpRequestsTotal.WithLabelValues(method, path, strconv.Itoa(status)).Inc()
	httpRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
}

// RecordNotificationEnqueued records a notification enqueue event
func RecordNotificationEnqueued(tenantID, channel string) {
	notificationsEnqueued.WithLabelValues(tenantID, channel).Inc()
}

// RecordNotificationProcessed records notification processing result
func RecordNotificationProcessed(status, channel string) {
	notificationsProcessed.WithLabelValues(status, channel).Inc()
}

// RecordNotificationLatency records end-to-end notification delivery time
func RecordNotificationLatency(channel string, latency time.Duration) {
	notificationLatency.WithLabelValues(channel).Observe(latency.Seconds())
}

// SetSQSMessagesInFlight sets the current in-flight message count
func SetSQSMessagesInFlight(count int) {
	sqsMessagesInFlight.Set(float64(count))
}

// RecordIdempotencyHit records a cache hit for idempotency
func RecordIdempotencyHit() {
	idempotencyHits.Inc()
}

// RecordRateLimitRejection records a rate limit rejection
func RecordRateLimitRejection(tenantID string) {
	rateLimitRejections.WithLabelValues(tenantID).Inc()
}

// SetDBConnections sets active database connection count
func SetDBConnections(count int) {
	dbConnectionsActive.Set(float64(count))
}

// SetRedisConnections sets active Redis connection count
func SetRedisConnections(count int) {
	redisConnectionsActive.Set(float64(count))
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Middleware returns HTTP middleware that records request metrics
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		RecordRequest(r.Method, r.URL.Path, wrapped.status, time.Since(start))
	})
}
