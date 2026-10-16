package handlers

import (
	"context"
	"log"
	"strings"

	"github.com/c12s/meridian/internal/domain"
	"github.com/c12s/meridian/pkg/api"
	pulsar_api "github.com/c12s/pulsar/model/protobuf"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)


func (m MeridianGrpcHandler) sendSeccompProfile(ctx context.Context, strategy string, metadata domain.SeccompProfile, profileDefinition *api.SeccompProfile, parent *domain.Namespace) error {
	ctx = setOutgoingContext(ctx)
	switch strings.ToLower(strategy) {
	case "redefine":
		profile := &pulsar_api.SeccompProfileDefinitionRequest{
			Profile: &pulsar_api.SeccompProfile{
				Namespace:    metadata.Namespace,
				Application:  metadata.Application,
				Name:         metadata.Name,
				Version:      metadata.Version,
				Architecture: metadata.Architecture,
			},
			Definition: &pulsar_api.SeccompProfileDefinition{
				DefaultAction: profileDefinition.DefaultAction,
				Architectures: []string{metadata.Architecture},
			},
		}
		for _, syscall := range profileDefinition.Syscalls {
			profile.Definition.Syscalls = append(profile.Definition.Syscalls, &pulsar_api.Syscalls{
				Names:  syscall.Names,
				Action: syscall.Action,
			})
		}
		_, err := m.pulsar.DefineSeccompProfile(ctx, profile)
		if err != nil {
			return err
		}
	case "extend":
		if parent == nil {
			return status.Error(codes.InvalidArgument, "cannot inherit or extend seccomp profiles - there is no parent")
		}
		profile := &pulsar_api.ExtendSeccompProfileRequest{
			ExtendProfile: &pulsar_api.SeccompProfile{
				Namespace:    parent.GetId(),
				Application:  parent.GetSeccompProfile().Application,
				Name:         parent.GetSeccompProfile().Name,
				Version:      parent.GetSeccompProfile().Version,
				Architecture: parent.GetSeccompProfile().Architecture,
			},
			DefineProfile: &pulsar_api.SeccompProfile{
				Namespace:    metadata.Namespace,
				Application:  metadata.Application,
				Name:         metadata.Name,
				Version:      metadata.Version,
				Architecture: metadata.Architecture,
			},
		}
		for _, syscall := range profileDefinition.Syscalls {
			profile.Syscalls = append(profile.Syscalls, &pulsar_api.Syscalls{
				Names:  syscall.Names,
				Action: syscall.Action,
			})
		}
		_, err := m.pulsar.ExtendSeccompProfile(ctx, profile)
		if err != nil {
			return err
		}
	default:
		if parent == nil {
			return status.Error(codes.InvalidArgument, "cannot inherit or extend seccomp profiles - there is no parent")
		}
		profile, err := m.pulsar.GetSeccompProfile(ctx, &pulsar_api.SeccompProfile{
			Namespace:    parent.GetSeccompProfile().Namespace,
			Application:  parent.GetSeccompProfile().Application,
			Name:         parent.GetSeccompProfile().Name,
			Version:      parent.GetSeccompProfile().Version,
			Architecture: parent.GetSeccompProfile().Architecture,
		})
		if err != nil {
			return err
		}
		_, err = m.pulsar.DefineSeccompProfile(ctx, &pulsar_api.SeccompProfileDefinitionRequest{
			Profile: &pulsar_api.SeccompProfile{
				Namespace:    metadata.Namespace,
				Application:  metadata.Application,
				Name:         metadata.Name,
				Version:      metadata.Version,
				Architecture: metadata.Architecture,
			},
			Definition: profile.Definition,
		})
		if err != nil {
			return err
		}
	}
	return nil
}


func (m MeridianGrpcHandler) getSeccompProfile(ctx context.Context, metadata domain.SeccompProfile) *api.SeccompProfile {
	ctx = setOutgoingContext(ctx)
	resp, err := m.pulsar.GetSeccompProfile(ctx, &pulsar_api.SeccompProfile{
		Namespace:    metadata.Namespace,
		Application:  metadata.Application,
		Name:         metadata.Name,
		Version:      metadata.Version,
		Architecture: metadata.Architecture,
	})
	if err != nil {
		log.Println(err)
		return nil
	}
	profile := &api.SeccompProfile{
		Version:       metadata.Version,
		DefaultAction: resp.Definition.DefaultAction,
	}
	for _, syscall := range resp.Definition.Syscalls {
		profile.Syscalls = append(profile.Syscalls, &api.SyscallRule{
			Names:  syscall.Names,
			Action: syscall.Action,
		})
	}
	return profile
}
