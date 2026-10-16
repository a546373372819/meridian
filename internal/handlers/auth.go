package handlers

import (
	"context"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// perm is one permission check
type perm struct {
	name, kind, id string
}

// authorize checks every permission
func (m MeridianGrpcHandler) authorize(ctx context.Context, perms ...perm) error {
	for _, p := range perms {
		if err := m.authorizer.Authorize(ctx, p.name, p.kind, p.id); err != nil {
			log.Printf("authz denied: %s|%s|%s: %v", p.name, p.kind, p.id, err)
			return status.Errorf(codes.PermissionDenied, "missing permission %s on %s %s", p.name, p.kind, p.id)
		}
	}
	return nil
}

// forwards the incoming metadata
func setOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		log.Println("[WARN] no metadata in ctx when sending req")
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, md)
}

// copies the callers authz token from the request metadata
func GetAuthInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if ok && len(md.Get("authz-token")) > 0 {
			ctx = context.WithValue(ctx, "authz-token", md.Get("authz-token")[0])
		}
		return handler(ctx, req)
	}
}
