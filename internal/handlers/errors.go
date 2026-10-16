package handlers

import (
	"github.com/c12s/meridian/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// turns an error from the services or stores into a gRPC status
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch domain.KindOf(err) {
	case domain.KindInvalid:
		return status.Error(codes.InvalidArgument, err.Error())
	case domain.KindNotFound:
		return status.Error(codes.NotFound, err.Error())
	case domain.KindPrecondition:
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
