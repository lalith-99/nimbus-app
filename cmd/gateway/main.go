package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/api"
	"github.com/lalithlochan/nimbus/internal/config"
	"github.com/lalithlochan/nimbus/internal/db"
	"github.com/lalithlochan/nimbus/internal/metrics"
	"github.com/lalithlochan/nimbus/internal/observ"
	"github.com/lalithlochan/nimbus/internal/redis"
	"github.com/lalithlochan/nimbus/internal/sqs"
	"github.com/lalithlochan/nimbus/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Setup logger
	logger, err := observ.NewLogger(cfg.Env, cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("starting nimbus gateway",
		zap.String("env", cfg.Env),
		zap.Int("port", cfg.Port),
		zap.String("version", "v1.0.1"),
		zap.String("deployment_test-2026-01-28", "true"),
	)

	// Initialize database connection
	ctx := context.Background()
	dbConfig := db.Config{
		Host:     cfg.DBHost,
		Port:     cfg.DBPort,
		User:     cfg.DBUser,
		Password: cfg.DBPassword,
		Database: cfg.DBName,
		SSLMode:  cfg.DBSSLMode,
	}

	database, err := db.New(ctx, dbConfig, logger)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	logger.Info("database connection established",
		zap.String("host", cfg.DBHost),
		zap.Int("port", cfg.DBPort),
		zap.String("database", cfg.DBName),
	)

	// Initialize repository
	repo := db.NewRepository(database, logger)

	// Initialize Redis for idempotency and rate limiting
	redisConfig := redis.Config{
		Host:     cfg.RedisHost,
		Port:     cfg.RedisPort,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}

	redisClient, err := redis.New(ctx, redisConfig, logger)
	if err != nil {
		logger.Warn("redis unavailable, idempotency disabled",
			zap.Error(err),
			zap.String("host", cfg.RedisHost),
		)
	}

	var idempotencyService *redis.IdempotencyService
	var rateLimiter *redis.RateLimiter
	if redisClient != nil {
		idempotencyService = redis.NewIdempotencyService(redisClient, logger)
		rateLimiter = redis.NewRateLimiter(redisClient, logger, redis.RateLimitConfig{
			Limit:  100,             // 100 requests
			Window: 1 * time.Minute, // per minute per tenant
		})
		defer redisClient.Close()
	}

	// Initialize SQS producer
	var producer *sqs.Producer
	if cfg.SQSQueueURL != "" {
		sqsCfg := sqs.Config{
			Region:   cfg.SQSRegion,
			QueueURL: cfg.SQSQueueURL,
			DLQURL:   cfg.SQSDLQURL,
		}
		producer, err = sqs.NewProducer(ctx, sqsCfg, logger)
		if err != nil {
			logger.Warn("sqs producer unavailable, events will not be enqueued",
				zap.Error(err),
			)
		}
		defer producer.Close()
	}

	sesCfg := worker.SESConfig{
		Region:    cfg.AWSRegion,
		FromEmail: cfg.SESFromEmail,
	}

	sender, err := worker.NewSESSender(ctx, sesCfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create SES email sender: %w", err)
	}

	// Initialize SNS sender for SMS
	snsSender, err := worker.NewSNSSender(ctx, worker.SNSConfig{
		Region: cfg.SNSRegion,
	}, logger)
	if err != nil {
		logger.Warn("SNS sender unavailable, SMS notifications disabled",
			zap.Error(err),
		)
		snsSender = nil
	}

	// Initialize webhook sender
	webhookSender := worker.NewWebhookSender(logger, worker.WebhookConfig{
		DefaultTimeout: time.Duration(cfg.WebhookTimeout) * time.Second,
	})

	// Create multi-sender that routes to appropriate channel handler
	var multiSender worker.Sender
	if snsSender != nil {
		multiSender = worker.NewMultiSender(logger, sender, snsSender, webhookSender)
	} else {
		// Fall back to email and webhook only if SNS unavailable
		multiSender = worker.NewMultiSender(logger, sender, webhookSender)
	}

	logger.Info("initialized multi-channel notification system",
		zap.Bool("email_enabled", true),
		zap.Bool("sms_enabled", snsSender != nil),
		zap.Bool("webhook_enabled", true),
	)

	w := worker.New(repo, multiSender, worker.Config{
		PollInterval: 5 * time.Second,
		BatchSize:    10,
		MaxRetries:   5,
	}, logger)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	go w.Start(workerCtx)

	logger.Info("background worker started")

	// Setup router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(metrics.Middleware)

	// Custom logging middleware
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("request completed",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Duration("duration_ms", time.Since(start)),
				zap.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	})

	// API routes
	var handler *api.Handler
	if idempotencyService != nil && producer != nil {
		handler = api.NewHandlerWithSQS(logger, repo, idempotencyService, producer)
	} else if idempotencyService != nil {
		handler = api.NewHandlerWithIdempotency(logger, repo, idempotencyService)
	} else {
		handler = api.NewHandler(logger, repo)
	}
	r.Route("/v1", func(r chi.Router) {
		// Apply rate limiting to API routes
		r.Use(api.RateLimitMiddleware(rateLimiter, logger, api.TenantKeyFunc))

		r.Post("/notifications", handler.CreateNotification)
		r.Get("/notifications", handler.ListNotifications)
		r.Get("/notifications/{id}", handler.GetNotification)
		r.Patch("/notifications/{id}/status", handler.UpdateNotificationStatus)

		// Dead Letter Queue routes
		r.Get("/dlq", handler.ListDeadLetterQueue)
		r.Get("/dlq/{id}", handler.GetDeadLetterItem)
		r.Post("/dlq/{id}/retry", handler.RetryDeadLetterItem)
		r.Post("/dlq/{id}/discard", handler.DiscardDeadLetterItem)
	})

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Prometheus metrics endpoint
	r.Handle("/metrics", metrics.Handler())

	// Setup HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", zap.String("addr", srv.Addr))
		serverErrors <- srv.ListenAndServe()
	}()

	// Listen for shutdown signals
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Block until we receive a signal or server error
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdown:
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))

		// Give outstanding requests 10 seconds to complete
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}

		logger.Info("server stopped gracefully")
	}

	return nil
}
