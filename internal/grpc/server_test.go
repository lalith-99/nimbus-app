package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lalithlochan/nimbus/internal/db"
	notificationv1 "github.com/lalithlochan/nimbus/proto/notification/v1"
)

// mockRepo is an in-memory NotificationRepository for testing the gRPC server.
type mockRepo struct {
	created *db.Notification
	stored  map[uuid.UUID]*db.Notification
}

func newMockRepo() *mockRepo {
	return &mockRepo{stored: make(map[uuid.UUID]*db.Notification)}
}

func (m *mockRepo) CreateNotification(_ context.Context, n *db.Notification) error {
	m.created = n
	m.stored[n.ID] = n
	return nil
}

func (m *mockRepo) GetNotification(_ context.Context, id uuid.UUID) (*db.Notification, error) {
	if n, ok := m.stored[id]; ok {
		return n, nil
	}
	return nil, errors.New("not found")
}

// ctxWithTenant mimics what the auth interceptor injects after validating a token.
func ctxWithTenant(tenant string) context.Context {
	return context.WithValue(context.Background(), ContextKeyTenantID, tenant)
}

const (
	tenantA  = "00000000-0000-0000-0000-0000000000aa"
	tenantB  = "00000000-0000-0000-0000-0000000000bb"
	someUser = "00000000-0000-0000-0000-000000000099"
)

// TestCreateNotification_TenantFromContext verifies the happy path: the tenant
// comes from the authenticated context and the notification is created for it.
func TestCreateNotification_TenantFromContext(t *testing.T) {
	repo := newMockRepo()
	srv := NewServer(repo, zap.NewNop())

	resp, err := srv.CreateNotification(ctxWithTenant(tenantA), &notificationv1.CreateNotificationRequest{
		TenantId: tenantA, // matches auth tenant
		UserId:   someUser,
		Channel:  "email",
		Payload:  []byte(`{"to":"x@y.com","body":"hi"}`),
	})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if resp.Id == "" {
		t.Fatal("expected a notification id")
	}
	if repo.created.TenantID.String() != tenantA {
		t.Errorf("notification created for wrong tenant: %s", repo.created.TenantID)
	}
}

// TestCreateNotification_RejectsTenantMismatch is the core IDOR regression test:
// a caller authenticated as tenant A must NOT be able to create a notification
// for tenant B by putting B's id in the request body.
func TestCreateNotification_RejectsTenantMismatch(t *testing.T) {
	repo := newMockRepo()
	srv := NewServer(repo, zap.NewNop())

	_, err := srv.CreateNotification(ctxWithTenant(tenantA), &notificationv1.CreateNotificationRequest{
		TenantId: tenantB, // attacker tries to act as another tenant
		UserId:   someUser,
		Channel:  "email",
		Payload:  []byte(`{}`),
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for tenant mismatch, got: %v", err)
	}
	if repo.created != nil {
		t.Fatal("no notification should have been created on tenant mismatch")
	}
}

// TestCreateNotification_RequiresAuthenticatedTenant ensures we fail closed when
// the interceptor didn't run / no tenant is present.
func TestCreateNotification_RequiresAuthenticatedTenant(t *testing.T) {
	repo := newMockRepo()
	srv := NewServer(repo, zap.NewNop())

	_, err := srv.CreateNotification(context.Background(), &notificationv1.CreateNotificationRequest{
		TenantId: tenantA,
		UserId:   someUser,
		Channel:  "email",
		Payload:  []byte(`{}`),
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated when no tenant in context, got: %v", err)
	}
}

// TestGetNotification_BlocksCrossTenantRead verifies a caller can't read another
// tenant's notification by guessing its UUID — we return NotFound (not
// PermissionDenied) to avoid leaking that the ID exists.
func TestGetNotification_BlocksCrossTenantRead(t *testing.T) {
	repo := newMockRepo()
	srv := NewServer(repo, zap.NewNop())

	// Seed a notification owned by tenant B.
	owner := uuid.MustParse(tenantB)
	id := uuid.New()
	repo.stored[id] = &db.Notification{ID: id, TenantID: owner, Channel: "email", Status: "pending"}

	// Tenant A tries to read it.
	_, err := srv.GetNotification(ctxWithTenant(tenantA), &notificationv1.GetNotificationRequest{Id: id.String()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for cross-tenant read, got: %v", err)
	}

	// Tenant B (the owner) can read it.
	got, err := srv.GetNotification(ctxWithTenant(tenantB), &notificationv1.GetNotificationRequest{Id: id.String()})
	if err != nil {
		t.Fatalf("owner read should succeed, got: %v", err)
	}
	if got.Id != id.String() {
		t.Errorf("expected id %s, got %s", id, got.Id)
	}
}
