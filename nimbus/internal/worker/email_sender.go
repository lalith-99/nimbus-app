package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/smtp"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

type EmailPayload struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

type EmailSender struct {
	config SMTPConfig
	logger *zap.Logger
}

func NewEmailSender(cfg SMTPConfig, logger *zap.Logger) *EmailSender {
	return &EmailSender{
		config: cfg,
		logger: logger,
	}
}

func (s *EmailSender) Send(ctx context.Context, notif *db.Notification) error {
	switch notif.Channel {
	case "email":
		return s.sendEmail(ctx, notif)
	case "sms":
		s.logger.Info("sms not implemented, skipping", zap.String("id", notif.ID.String()))
		return nil
	case "webhook":
		s.logger.Info("webhook not implemented, skipping", zap.String("id", notif.ID.String()))
		return nil
	default:
		return fmt.Errorf("unsupported channel: %s", notif.Channel)
	}
}

func (s *EmailSender) sendEmail(ctx context.Context, notif *db.Notification) error {
	var payload EmailPayload
	if err := json.Unmarshal(notif.Payload, &payload); err != nil {
		return fmt.Errorf("invalid email payload: %w", err)
	}

	if payload.To == "" {
		return fmt.Errorf("email 'to' field is required")
	}

	// Build email message
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		s.config.From,
		payload.To,
		payload.Subject,
		payload.Body,
	)

	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)

	var auth smtp.Auth
	if s.config.Username != "" {
		auth = smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
	}

	err := smtp.SendMail(addr, auth, s.config.From, []string{payload.To}, []byte(msg))
	if err != nil {
		return fmt.Errorf("smtp send failed: %w", err)
	}

	s.logger.Info("email sent",
		zap.String("id", notif.ID.String()),
		zap.String("to", payload.To),
	)

	return nil
}
