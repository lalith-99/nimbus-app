package sns

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
)

// Channel represents notification delivery channel
type Channel string

const (
	ChannelEmail   Channel = "email"
	ChannelSMS     Channel = "sms"
	ChannelWebhook Channel = "webhook"
)

// Publisher handles SNS topic publishing for multi-channel routing
type Publisher struct {
	client   *sns.Client
	topicARN string
}

// Message represents a notification message for SNS
type Message struct {
	NotificationID string            `json:"notification_id"`
	TenantID       string            `json:"tenant_id"`
	Channel        Channel           `json:"channel"`
	Recipient      string            `json:"recipient"`
	Subject        string            `json:"subject,omitempty"`
	Body           string            `json:"body"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// NewPublisher creates an SNS publisher for the given topic
func NewPublisher(ctx context.Context, topicARN string, optFns ...func(*config.LoadOptions) error) (*Publisher, error) {
	cfg, err := config.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Publisher{
		client:   sns.NewFromConfig(cfg),
		topicARN: topicARN,
	}, nil
}

// NewPublisherWithEndpoint creates a publisher with custom endpoint (for LocalStack)
func NewPublisherWithEndpoint(ctx context.Context, topicARN, endpoint, region string) (*Publisher, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := sns.NewFromConfig(cfg, func(o *sns.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	return &Publisher{
		client:   client,
		topicARN: topicARN,
	}, nil
}

// Publish sends a message to SNS with channel-based routing
func (p *Publisher) Publish(ctx context.Context, msg Message) (string, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	input := &sns.PublishInput{
		TopicArn: aws.String(p.topicARN),
		Message:  aws.String(string(payload)),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"channel": {
				DataType:    aws.String("String"),
				StringValue: aws.String(string(msg.Channel)),
			},
			"tenant_id": {
				DataType:    aws.String("String"),
				StringValue: aws.String(msg.TenantID),
			},
		},
	}

	result, err := p.client.Publish(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to publish to SNS: %w", err)
	}

	return *result.MessageId, nil
}

// PublishBatch sends multiple messages to SNS
func (p *Publisher) PublishBatch(ctx context.Context, messages []Message) ([]string, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	if len(messages) > 10 {
		return nil, fmt.Errorf("batch size exceeds SNS limit of 10")
	}

	entries := make([]types.PublishBatchRequestEntry, len(messages))
	for i, msg := range messages {
		payload, err := json.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal message %d: %w", i, err)
		}

		entries[i] = types.PublishBatchRequestEntry{
			Id:      aws.String(msg.NotificationID),
			Message: aws.String(string(payload)),
			MessageAttributes: map[string]types.MessageAttributeValue{
				"channel": {
					DataType:    aws.String("String"),
					StringValue: aws.String(string(msg.Channel)),
				},
				"tenant_id": {
					DataType:    aws.String("String"),
					StringValue: aws.String(msg.TenantID),
				},
			},
		}
	}

	result, err := p.client.PublishBatch(ctx, &sns.PublishBatchInput{
		TopicArn:                   aws.String(p.topicARN),
		PublishBatchRequestEntries: entries,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to publish batch to SNS: %w", err)
	}

	if len(result.Failed) > 0 {
		return nil, fmt.Errorf("partial batch failure: %d messages failed", len(result.Failed))
	}

	messageIDs := make([]string, len(result.Successful))
	for i, entry := range result.Successful {
		messageIDs[i] = *entry.MessageId
	}

	return messageIDs, nil
}
