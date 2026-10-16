package handlers

import (
	"errors"
	"fmt"
	"testing"

	"github.com/c12s/meridian/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatusCodes(t *testing.T) {
	cases := []struct {
		err  error
		want codes.Code
	}{
		{domain.Invalid("bad amount"), codes.InvalidArgument},
		{fmt.Errorf("read: %w", domain.NotFound("no app")), codes.NotFound},
		{domain.Precondition("only 2 cpu free"), codes.FailedPrecondition},
		{errors.New("neo4j down"), codes.Internal},
		{status.Error(codes.Unavailable, "pulsar down"), codes.Unavailable}, // kept as is
	}
	for _, c := range cases {
		if got := status.Code(toStatus(c.err)); got != c.want {
			t.Errorf("toStatus(%v) = %v, want %v", c.err, got, c.want)
		}
	}
	if toStatus(nil) != nil {
		t.Error("toStatus(nil) must be nil")
	}
}
