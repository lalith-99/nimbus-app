package grpc

import (
	"context"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ContextKey is a typed key to avoid collisions in context.WithValue.
// Using a custom type (not string) prevents other packages from accidentally
// reading/overwriting values — a subtle but important Go best practice.
type ContextKey string

const (
	ContextKeyTenantID ContextKey = "tenant_id"
)

// TenantIDFromContext returns the tenant_id that the auth interceptor
// validated and injected for this request.
//
// Handlers MUST use this — never trust a tenant_id from the request body —
// otherwise an authenticated caller for tenant A could read or write tenant B's
// data by simply putting B's id in the payload (a classic IDOR / broken
// access control bug, OWASP API1:2023). The token is the source of truth for
// identity; the request body only describes *what* to do, never *who* you are.
func TenantIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ContextKeyTenantID).(string)
	return v, ok && v != ""
}

// AuthInterceptor returns a gRPC UnaryServerInterceptor that validates
// Bearer tokens on every incoming unary RPC.
//
// Flow:
//
//	gRPC metadata (Authorization: Bearer <token>)
//	  → extract token
//	  → look up tenant_id
//	  → inject tenant_id into context
//	  → pass to handler
//
// Why a map[string]string for token→tenantID?
// For a portfolio project this is fine. In production you'd validate a JWT:
// 1. Verify signature (RS256 / HS256)
// 2. Check expiry (exp claim)
// 3. Extract tenant_id from claims
// The interceptor shape is identical — you'd swap the lookup for jwt.Parse().
//
// Interview talking point:
// "The auth interceptor is middleware at the gRPC layer, equivalent to
//
//	HTTP middleware. I inject tenant_id into the context so every handler
//	gets it without needing to re-parse the token. This is the same pattern
//	as context propagation in distributed tracing."
func AuthInterceptor(validTokens map[string]string, logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		tenantID, err := extractAndValidate(ctx, validTokens, logger)
		if err != nil {
			return nil, err
		}
		// Inject tenant_id into context — downstream handlers read it
		// via ctx.Value(ContextKeyTenantID) without touching the token again.
		ctx = context.WithValue(ctx, ContextKeyTenantID, tenantID)
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor does the same for server-streaming RPCs.
// gRPC requires separate interceptor types for unary vs streaming RPCs.
//
// Subtlety: a streaming handler reads ss.Context(), but ServerStream's context
// is read-only — we can't context.WithValue it directly like the unary path.
// So we wrap the stream in wrappedServerStream, overriding Context() to return
// one that carries the authenticated tenant. The handler then reads it the same
// way (TenantIDFromContext) regardless of unary vs streaming.
func StreamAuthInterceptor(validTokens map[string]string, logger *zap.Logger) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		tenantID, err := extractAndValidate(ss.Context(), validTokens, logger)
		if err != nil {
			return err
		}
		ctx := context.WithValue(ss.Context(), ContextKeyTenantID, tenantID)
		return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
	}
}

// wrappedServerStream overrides Context() so we can inject the authenticated
// tenant into a server-streaming RPC's context. Every other ServerStream method
// (Send, Recv, etc.) is inherited unchanged via embedding.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context { return w.ctx }

// extractAndValidate pulls the Bearer token from gRPC metadata and validates it.
// gRPC metadata is analogous to HTTP headers — it's key-value pairs sent
// alongside the RPC, carried in the HTTP/2 HEADERS frame.
func extractAndValidate(ctx context.Context, validTokens map[string]string, logger *zap.Logger) (string, error) {
	// metadata.FromIncomingContext retrieves the headers the client sent.
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Errorf(codes.Unauthenticated, "missing metadata")
	}

	// gRPC metadata keys are lowercase. "authorization" matches the HTTP standard.
	values := md.Get("authorization")
	if len(values) == 0 {
		return "", status.Errorf(codes.Unauthenticated, "missing authorization header")
	}

	// Strip "Bearer " prefix — same convention as HTTP Authorization header.
	token := strings.TrimPrefix(values[0], "Bearer ")
	token = strings.TrimSpace(token)

	tenantID, ok := validTokens[token]
	if !ok {
		// Log a prefix for debugging without logging the full secret.
		prefix := token
		if len(prefix) > 8 {
			prefix = prefix[:8] + "..."
		}
		logger.Warn("gRPC auth: invalid token",
			zap.String("token_prefix", prefix),
		)
		return "", status.Errorf(codes.Unauthenticated, "invalid token")
	}

	logger.Debug("gRPC auth: authenticated",
		zap.String("tenant_id", tenantID),
	)

	return tenantID, nil
}
