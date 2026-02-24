package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port     int
	LogLevel string
	Env      string

	// Database
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// Redis config
	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisDB       int

	// SQS config
	SQSRegion   string
	SQSQueueURL string
	SQSDLQURL   string

	// SMTP config for email sending
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string // sender email address

	// AWS Services
	AWSRegion    string
	SESFromEmail string
	SNSRegion    string // AWS region for SNS (SMS)

	// Webhook config
	WebhookTimeout int // Timeout for webhook requests in seconds

	// AI / OpenAI config
	AIEnabled    bool   // Enable AI features (compose endpoint + content enrichment)
	OpenAIAPIKey string // OpenAI API key
	OpenAIModel  string // Model to use (default: gpt-4o-mini)
}

// Load reads configuration from environment variables with sensible defaults
func Load() (*Config, error) {
	cfg := &Config{
		Port:     8080,
		LogLevel: "info",
		Env:      "development",

		// Local postgres defaults
		DBHost:     "localhost",
		DBPort:     5432,
		DBUser:     "lalithlochan",
		DBPassword: "",
		DBName:     "nimbus",
		DBSSLMode:  "disable",

		// Redis defaults
		RedisHost:     "localhost",
		RedisPort:     6379,
		RedisPassword: "",
		RedisDB:       0,

		// SMTP defaults
		SMTPHost: "localhost",
		SMTPPort: 587,
		SMTPFrom: "noreply@nimbus.local",

		AWSRegion:    "us-east-1",
		SESFromEmail: "noreply@nimbus.local",
	}

	if port := os.Getenv("PORT"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
		cfg.Port = p
	}

	if level := os.Getenv("LOG_LEVEL"); level != "" {
		cfg.LogLevel = level
	}

	if env := os.Getenv("ENV"); env != "" {
		cfg.Env = env
	}

	// Database config
	if host := os.Getenv("DB_HOST"); host != "" {
		cfg.DBHost = host
	}

	if port := os.Getenv("DB_PORT"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("invalid DB_PORT: %w", err)
		}
		cfg.DBPort = p
	}

	if user := os.Getenv("DB_USER"); user != "" {
		cfg.DBUser = user
	}

	if password := os.Getenv("DB_PASSWORD"); password != "" {
		cfg.DBPassword = password
	}

	if dbname := os.Getenv("DB_NAME"); dbname != "" {
		cfg.DBName = dbname
	}

	if sslmode := os.Getenv("DB_SSLMODE"); sslmode != "" {
		cfg.DBSSLMode = sslmode
	}

	// Redis config
	if host := os.Getenv("REDIS_HOST"); host != "" {
		cfg.RedisHost = host
	}

	if port := os.Getenv("REDIS_PORT"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_PORT: %w", err)
		}
		cfg.RedisPort = p
	}

	if password := os.Getenv("REDIS_PASSWORD"); password != "" {
		cfg.RedisPassword = password
	}

	if db := os.Getenv("REDIS_DB"); db != "" {
		d, err := strconv.Atoi(db)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB: %w", err)
		}
		cfg.RedisDB = d
	}

	if host := os.Getenv("SMTP_HOST"); host != "" {
		cfg.SMTPHost = host
	}

	if port := os.Getenv("SMTP_PORT"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("invalid SMTP_PORT: %w", err)
		}
		cfg.SMTPPort = p
	}

	if user := os.Getenv("SMTP_USERNAME"); user != "" {
		cfg.SMTPUsername = user
	}

	if pass := os.Getenv("SMTP_PASSWORD"); pass != "" {
		cfg.SMTPPassword = pass
	}

	if from := os.Getenv("SMTP_FROM"); from != "" {
		cfg.SMTPFrom = from
	}

	if region := os.Getenv("AWS_REGION"); region != "" {
		cfg.AWSRegion = region
	}

	if from := os.Getenv("SES_FROM_EMAIL"); from != "" {
		cfg.SESFromEmail = from
	}

	// SQS config
	if region := os.Getenv("SQS_REGION"); region != "" {
		cfg.SQSRegion = region
	} else {
		cfg.SQSRegion = cfg.AWSRegion
	}

	if url := os.Getenv("SQS_QUEUE_URL"); url != "" {
		cfg.SQSQueueURL = url
	}

	if url := os.Getenv("SQS_DLQ_URL"); url != "" {
		cfg.SQSDLQURL = url
	}

	// SNS config for SMS
	if region := os.Getenv("SNS_REGION"); region != "" {
		cfg.SNSRegion = region
	} else {
		cfg.SNSRegion = cfg.AWSRegion
	}

	// Webhook config
	if timeout := os.Getenv("WEBHOOK_TIMEOUT"); timeout != "" {
		t, err := strconv.Atoi(timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid WEBHOOK_TIMEOUT: %w", err)
		}
		cfg.WebhookTimeout = t
	} else {
		cfg.WebhookTimeout = 30 // default 30 seconds
	}

	// AI config
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		cfg.OpenAIAPIKey = key
		cfg.AIEnabled = true
	}
	if model := os.Getenv("OPENAI_MODEL"); model != "" {
		cfg.OpenAIModel = model
	} else {
		cfg.OpenAIModel = "gpt-4o-mini"
	}

	return cfg, nil
}
