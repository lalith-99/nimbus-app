package sqs_test

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
	"github.com/lalithlochan/nimbus/internal/sqs"
)

// These microbenchmarks exercise message construction and JSON serialization.
// They do not call Producer.Enqueue or measure HTTP, database, Redis, network,
// SQS, or notification-provider latency. A daily event-rate calculation is a
// workload assumption, not evidence of sustainable service capacity.

// buildSQSMessage builds the transport type used by Producer.Enqueue without
// invoking the AWS SDK. Keeping this scope explicit avoids treating a local
// serialization measurement as an end-to-end enqueue measurement.
func buildSQSMessage(notif *db.Notification) ([]byte, error) {
	msg := sqs.Message{
		NotificationID: notif.ID.String(),
		TenantID:       notif.TenantID.String(),
		UserID:         notif.UserID.String(),
		Channel:        notif.Channel,
		Payload:        notif.Payload,
		Attempt:        notif.Attempt,
		EnqueuedAt:     time.Now().UnixNano(),
	}
	return json.Marshal(msg)
}

func sampleNotification() *db.Notification {
	return &db.Notification{
		ID:       uuid.New(),
		TenantID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		UserID:   uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		Channel:  "email",
		Payload:  json.RawMessage(`{"to":"bench@example.com","subject":"Test","body":"Benchmark message"}`),
		Status:   db.StatusPending,
		Attempt:  0,
	}
}

// TestMessageSerializationTiming retains a coarse local regression budget for
// message construction plus marshaling. Results depend on the test machine and
// scheduler; this threshold is not an application or AWS service SLA.
func TestMessageSerializationTiming(t *testing.T) {
	const iterations = 1_000
	latencies := make([]time.Duration, 0, iterations)
	notif := sampleNotification()

	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := buildSQSMessage(notif)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("buildSQSMessage failed: %v", err)
		}
		latencies = append(latencies, elapsed)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[int(float64(len(latencies))*0.50)]
	p95 := latencies[int(float64(len(latencies))*0.95)]
	p99 := latencies[int(float64(len(latencies))*0.99)]
	t.Logf("Local message construction + JSON serialization over %d iterations: p50=%v p95=%v p99=%v", iterations, p50, p95, p99)
	t.Log("No HTTP, database, Redis, AWS transport, or provider delivery is measured.")

	const localP99Budget = 50 * time.Millisecond
	if p99 > localP99Budget {
		t.Errorf("local serialization p99 %v exceeds regression budget %v", p99, localP99Budget)
	}
}

// TestIllustrativeEventRateMath only converts an example daily workload to an
// average rate and a hypothetical burst rate. It does not run that workload.
func TestIllustrativeEventRateMath(t *testing.T) {
	const (
		eventsPerDay    = 100_000
		secondsPerDay   = 86_400
		burstMultiplier = 10
	)
	averageRate := float64(eventsPerDay) / float64(secondsPerDay)
	burstRate := averageRate * burstMultiplier
	t.Logf("Illustrative workload: %d events/day = %.2f events/sec average; assumed %dx burst = %.2f events/sec", eventsPerDay, averageRate, burstMultiplier, burstRate)
	t.Log("This arithmetic does not measure Nimbus throughput, concurrency, latency, or backlog behavior.")
	if averageRate <= 1 || averageRate >= 2 {
		t.Fatalf("unexpected example rate: %.2f events/sec", averageRate)
	}
}

// BenchmarkMessageSerialization measures the local message-build/marshal path.
// Run: go test -run '^$' -bench '^BenchmarkMessageSerialization$' -benchmem ./internal/sqs
// Record the Go version, machine, commit, payload, and command with any result.
// No reference result or production-capacity conclusion is embedded here.
func BenchmarkMessageSerialization(b *testing.B) {
	notif := sampleNotification()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := buildSQSMessage(notif); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUUID measures local UUID generation separately from serialization.
func BenchmarkUUID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = uuid.New()
	}
}

// TestLocalSQSEnqueueTiming is an opt-in probe of an already-created local SQS
// emulator queue. CI does not provision LocalStack. It sends test messages only
// when NIMBUS_TEST_SQS_QUEUE_URL is explicitly set to an HTTP loopback URL, e.g.
// http://localhost:4566/000000000000/nimbus-notifications. An emulator measurement
// is not an AWS latency result or a service capacity test.
func TestLocalSQSEnqueueTiming(t *testing.T) {
	queueURL := os.Getenv("NIMBUS_TEST_SQS_QUEUE_URL")
	if queueURL == "" {
		t.Skip("set NIMBUS_TEST_SQS_QUEUE_URL to opt in to a local SQS emulator probe")
	}
	u, err := url.Parse(queueURL)
	if err != nil {
		t.Fatalf("invalid local SQS queue URL: %v", err)
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || u.User != nil || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		t.Fatal("NIMBUS_TEST_SQS_QUEUE_URL must use HTTP and a loopback host")
	}

	// Use a local endpoint and dummy credentials instead of ambient AWS settings.
	endpoint := (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	t.Setenv("AWS_ENDPOINT_URL_SQS", endpoint)
	t.Setenv("AWS_IGNORE_CONFIGURED_ENDPOINT_URLS", "false")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	producer, err := sqs.NewProducer(ctx, sqs.Config{Region: "us-east-1", QueueURL: queueURL}, zap.NewNop())
	if err != nil {
		t.Fatalf("initialize explicitly configured local producer: %v", err)
	}
	defer producer.Close()

	notif := sampleNotification()
	const samples = 100
	latencies := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		start := time.Now()
		if _, err := producer.Enqueue(ctx, notif); err != nil {
			t.Fatalf("enqueue to explicitly configured local emulator: %v", err)
		}
		latencies = append(latencies, time.Since(start))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p99 := latencies[int(float64(len(latencies))*0.99)]
	t.Logf("Local emulator enqueue p99 over %d samples: %v; no production SLA inferred", samples, p99)
}
