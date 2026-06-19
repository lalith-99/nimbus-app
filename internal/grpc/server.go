package grpc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/lalithlochan/nimbus/internal/db"
	notificationv1 "github.com/lalithlochan/nimbus/proto/notification/v1"
)

// Server implements the generated NotificationServiceServer interface.
//
// Architecture note — why embed UnimplementedNotificationServiceServer?
// protoc generates a struct with all RPCs returning codes.Unimplemented.
// Embedding it means: if we add a new RPC to the proto later, the server
// still compiles — it just returns UNIMPLEMENTED instead of panicking.
// This is the standard forward-compatibility pattern in Go gRPC.
type Server struct {
	notificationv1.UnimplementedNotificationServiceServer
	repo   NotificationRepository
	logger *zap.Logger
}

// NotificationRepository is the subset of DB operations the gRPC server needs.
// We define our own interface (not importing db.Repository directly) to keep
// this package loosely coupled and easily mockable in tests.
type NotificationRepository interface {
	CreateNotification(ctx context.Context, notif *db.Notification) error
	GetNotification(ctx context.Context, id uuid.UUID) (*db.Notification, error)
}

// NewServer creates the gRPC service implementation.
func NewServer(repo NotificationRepository, logger *zap.Logger) *Server {
	return &Server{repo: repo, logger: logger}
}

// ─── RPCs ────────────────────────────────────────────────────────────────────

// CreateNotification — Unary RPC.
// Mirrors POST /v1/notifications but for internal callers.
// Advantages over REST: strong protobuf typing, binary encoding (~5x smaller
// than JSON), built-in deadline propagation via gRPC context.
func (s *Server) CreateNotification(
	ctx context.Context,
	req *notificationv1.CreateNotificationRequest,
) (*notificationv1.CreateNotificationResponse, error) {

	// SECURITY: the tenant is taken from the authenticated token (injected by the
	// auth interceptor), NOT from the request body. We still parse the body's
	// tenant_id, but only to reject mismatches — a caller authenticated as tenant A
	// must not be able to create notifications for tenant B (IDOR / OWASP API1).
	authTenant, ok := TenantIDFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "no authenticated tenant in context")
	}
	if req.TenantId != "" && req.TenantId != authTenant {
		s.logger.Warn("gRPC: tenant mismatch blocked",
			zap.String("auth_tenant", authTenant),
			zap.String("body_tenant", req.TenantId),
		)
		return nil, status.Errorf(codes.PermissionDenied,
			"tenant_id in request does not match authenticated tenant")
	}

	tenantID, err := uuid.Parse(authTenant)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "authenticated tenant is not a valid UUID: %v", err)
	}
	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user_id: %v", err)
	}

	validChannels := map[string]bool{"email": true, "sms": true, "webhook": true}
	if !validChannels[req.Channel] {
		return nil, status.Errorf(codes.InvalidArgument, "channel must be email, sms, or webhook")
	}

	notif := &db.Notification{
		ID:       uuid.New(),
		TenantID: tenantID,
		UserID:   userID,
		Channel:  req.Channel,
		Payload:  req.Payload,
		Status:   db.StatusPending,
		Attempt:  0,
	}

	if err := s.repo.CreateNotification(ctx, notif); err != nil {
		s.logger.Error("gRPC CreateNotification: DB write failed",
			zap.Error(err),
			zap.String("tenant_id", req.TenantId),
		)
		return nil, status.Errorf(codes.Internal, "failed to create notification")
	}

	s.logger.Info("gRPC: notification created",
		zap.String("id", notif.ID.String()),
		zap.String("channel", req.Channel),
	)

	return &notificationv1.CreateNotificationResponse{
		Id:        notif.ID.String(),
		Status:    notif.Status,
		CreatedAt: timestamppb.New(notif.CreatedAt),
	}, nil
}

// GetNotification — Unary RPC.
func (s *Server) GetNotification(
	ctx context.Context,
	req *notificationv1.GetNotificationRequest,
) (*notificationv1.Notification, error) {

	authTenant, ok := TenantIDFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "no authenticated tenant in context")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	notif, err := s.repo.GetNotification(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "notification not found: %v", err)
	}

	// SECURITY: enforce tenant ownership. We return NotFound rather than
	// PermissionDenied so we don't leak the existence of another tenant's
	// notification IDs (avoids an enumeration oracle).
	if notif.TenantID.String() != authTenant {
		s.logger.Warn("gRPC: cross-tenant read blocked",
			zap.String("auth_tenant", authTenant),
			zap.String("owner_tenant", notif.TenantID.String()),
		)
		return nil, status.Errorf(codes.NotFound, "notification not found")
	}

	return toProto(notif), nil
}

// StreamDeliveryUpdates — Server-streaming RPC.
//
// The client opens ONE persistent HTTP/2 stream and the server pushes status
// updates every 2 seconds until a terminal state is reached.
//
// Why server-streaming over polling?
// Classic client-side polling: client fires GET /notifications/{id} every N seconds.
// That's N requests × duration regardless of whether anything changed.
// Server-streaming: ONE request, server drives the cadence. O(1) requests total.
// On a 100K events/day system, eliminating polling saves thousands of requests/day.
//
// Implementation note on internal polling:
// We poll the DB every 2s rather than using Postgres LISTEN/NOTIFY because
// LISTEN/NOTIFY requires a persistent, non-pooled DB connection per active stream —
// doesn't scale. DB polling with a short interval works fine for v1 and scales
// horizontally (each replica polls independently).
func (s *Server) StreamDeliveryUpdates(
	req *notificationv1.StreamDeliveryUpdatesRequest,
	stream notificationv1.NotificationService_StreamDeliveryUpdatesServer,
) error {

	ctx := stream.Context()

	authTenant, ok := TenantIDFromContext(ctx)
	if !ok {
		return status.Errorf(codes.Unauthenticated, "no authenticated tenant in context")
	}

	id, err := uuid.Parse(req.NotificationId)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid notification_id: %v", err)
	}

	// SECURITY: verify ownership ONCE up front before opening the stream.
	// Without this, a caller could stream another tenant's delivery updates
	// by guessing a notification UUID.
	owner, err := s.repo.GetNotification(ctx, id)
	if err != nil {
		return status.Errorf(codes.NotFound, "notification not found")
	}
	if owner.TenantID.String() != authTenant {
		s.logger.Warn("gRPC: cross-tenant stream blocked",
			zap.String("auth_tenant", authTenant),
			zap.String("owner_tenant", owner.TenantID.String()),
		)
		return status.Errorf(codes.NotFound, "notification not found")
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Terminal states: once reached there are no more status transitions.
	// We close the stream here so the client doesn't need to timeout.
	terminalStates := map[string]bool{
		"sent":          true,
		"failed":        true,
		"dead_lettered": true,
	}

	for {
		select {
		case <-ctx.Done():
			// Client disconnected or cancelled — normal, not an error on our side.
			return ctx.Err()

		case <-ticker.C:
			notif, err := s.repo.GetNotification(ctx, id)
			if err != nil {
				return status.Errorf(codes.Internal, "failed to poll notification: %v", err)
			}

			update := &notificationv1.DeliveryUpdate{
				NotificationId: notif.ID.String(),
				Status:         notif.Status,
				Attempt:        int32(notif.Attempt),
				UpdatedAt:      timestamppb.Now(),
			}
			if notif.ErrorMessage != nil {
				update.ErrorMessage = *notif.ErrorMessage
			}

			// stream.Send pushes the message to the client over the open HTTP/2 stream.
			// Returns an error if the client disconnected mid-stream.
			if err := stream.Send(update); err != nil {
				return err
			}

			s.logger.Debug("gRPC: delivery update streamed",
				zap.String("id", notif.ID.String()),
				zap.String("status", notif.Status),
			)

			if terminalStates[notif.Status] {
				s.logger.Info("gRPC: stream closed — terminal state",
					zap.String("id", notif.ID.String()),
					zap.String("status", notif.Status),
				)
				return nil
			}
		}
	}
}

// toProto converts a DB model to a protobuf message.
// Keeping conversion logic in a single helper avoids duplication and makes
// the mapping easy to audit when the schema changes.
func toProto(n *db.Notification) *notificationv1.Notification {
	p := &notificationv1.Notification{
		Id:        n.ID.String(),
		TenantId:  n.TenantID.String(),
		UserId:    n.UserID.String(),
		Channel:   n.Channel,
		Status:    n.Status,
		Attempt:   int32(n.Attempt),
		CreatedAt: timestamppb.New(n.CreatedAt),
		UpdatedAt: timestamppb.New(n.UpdatedAt),
	}
	if n.ErrorMessage != nil {
		p.ErrorMsg = *n.ErrorMessage
	}
	return p
}
