package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

// SNSSender sends SMS notifications via AWS SNS
type SNSSender struct {
	client *sns.Client
	logger *zap.Logger
}

type SNSConfig struct {
	Region string
}

// NewSNSSender creates a new SNS sender for SMS notifications
func NewSNSSender(ctx context.Context, cfg SNSConfig, logger *zap.Logger) (*SNSSender, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load default AWS config for SNS: %w", err)
	}

	return &SNSSender{
		client: sns.NewFromConfig(awsCfg),
		logger: logger,
	}, nil
}

// Send sends an SMS notification via AWS SNS
func (s *SNSSender) Send(ctx context.Context, notif *db.Notification) error {
	if notif.Channel != db.ChannelSMS {
		return fmt.Errorf("SNS sender only supports SMS, got: %s", notif.Channel)
	}

	// Parse payload
	var payload SMSPayload
	if err := json.Unmarshal(notif.Payload, &payload); err != nil {
		return fmt.Errorf("invalid SMS payload: %w", err)
	}

	// Validate required fields
	if payload.PhoneNumber == "" {
		return fmt.Errorf("SMS payload missing phone_number")
	}
	if payload.Message == "" {
		return fmt.Errorf("SMS payload missing message")
	}

	// Send SMS via SNS
	input := &sns.PublishInput{
		PhoneNumber: aws.String(payload.PhoneNumber),
		Message:     aws.String(payload.Message),
	}

	result, err := s.client.Publish(ctx, input)
	if err != nil {
		return fmt.Errorf("sns publish failed: %w", err)
	}

	s.logger.Info("SMS sent via SNS",
		zap.String("id", notif.ID.String()),
		zap.String("phone_number", payload.PhoneNumber),
		zap.String("message_id", *result.MessageId),
	)

	return nil
}

// SupportsChannel checks if this sender supports the SMS channel
func (s *SNSSender) SupportsChannel(channel string) bool {
	return channel == db.ChannelSMS
}
