package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/lalithlochan/nimbus/internal/db"
	"go.uber.org/zap"
)

type SESSender struct {
	client *ses.Client
	from   string
	logger *zap.Logger
}

type SESConfig struct {
	Region    string
	FromEmail string
}

func NewSESSender(ctx context.Context, cfg SESConfig, logger *zap.Logger) (*SESSender, error) {

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load default AWS config: %w", err)
	}
	return &SESSender{
		// Initialize fields
		client: ses.NewFromConfig(awsCfg),
		from:   cfg.FromEmail,
		logger: logger,
	}, nil
}

// Send sends an email notification via AWS SES
func (s *SESSender) Send(ctx context.Context, notif *db.Notification) error {
	// Validate channel
	if notif.Channel != db.ChannelEmail {
		return fmt.Errorf("SES sender only supports email, got: %s", notif.Channel)
	}

	// Parse Payload
	var payload EmailPayload
	if err := json.Unmarshal(notif.Payload, &payload); err != nil {
		return fmt.Errorf("invalid email payload: %w", err)
	}

	// Validate required fields
	if payload.To == "" {
		return fmt.Errorf("email payload missing 'to' field")
	}
	if payload.Subject == "" {
		return fmt.Errorf("email payload missing 'subject' field")
	}
	if payload.Body == "" {
		return fmt.Errorf("email payload missing 'body' field")
	}

	// Build SES input
	input := &ses.SendEmailInput{
		Source: aws.String(s.from),
		Destination: &types.Destination{
			ToAddresses: []string{payload.To},
		},
		Message: &types.Message{
			Subject: &types.Content{
				Data:    aws.String(payload.Subject),
				Charset: aws.String("UTF-8"),
			},
			Body: &types.Body{
				Text: &types.Content{
					Data:    aws.String(payload.Body),
					Charset: aws.String("UTF-8"),
				},
			},
		},
	}

	// Send
	result, err := s.client.SendEmail(ctx, input)
	if err != nil {
		return fmt.Errorf("ses send failed: %w", err)
	}

	s.logger.Info("email sent via SES",
		zap.String("id", notif.ID.String()),
		zap.String("to", payload.To),
		zap.String("message_id", *result.MessageId),
	)

	return nil
}

// SupportsChannel checks if this sender supports the email channel
func (s *SESSender) SupportsChannel(channel string) bool {
	return channel == db.ChannelEmail
}
