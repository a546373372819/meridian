package handlers

import (
	"context"
	"encoding/json"
	"log"

	"github.com/c12s/meridian/internal/domain"
	"github.com/c12s/meridian/pkg/api"
	oortapi "github.com/c12s/oort/pkg/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (m MeridianGrpcHandler) AddApp(ctx context.Context, req *api.AddAppReq) (*api.AddAppResp, error) {
	nsId := domain.MakeNamespaceId(req.OrgId, req.Namespace)
	
	if err := m.authorize(ctx,
		perm{"org.namespace.get", "namespace", nsId},
		perm{"namespace.app.add", "namespace", nsId},
	); err != nil {
		return nil, err
	}

	namespace, err := m.namespaces.Get(nsId)
	if err != nil {
		log.Println(err)
		return nil, status.Error(codes.NotFound, "namespace not found")
	}
	app := domain.NewApp(namespace, req.Name, req.Profile.Version)
	for resource, quota := range req.Quotas {
		if err := app.AddResourceQuota(resource, quota); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	if err := m.sendSeccompProfile(ctx, req.SeccompDefinitionStrategy, app.GetSeccompProfile(), req.Profile, &namespace); err != nil {
		return nil, toStatus(err)
	}
	if err := m.apps.Add(app); err != nil {
		log.Println(err)
		return nil, toStatus(err)
	}
	profile, err := json.MarshalIndent(m.getSeccompProfile(ctx, app.GetSeccompProfile()), "", "\t")
	if err != nil {
		return nil, toStatus(err)
	}
	cmd := api.ApplyAppConfigCommand{
		OrgId:          req.OrgId,
		NamespaceName:  req.Namespace,
		AppName:        req.Name,
		SeccompProfile: string(profile),
		Quotas:         req.Quotas,
	}
	
	if err := m.nodes.Disseminate(ctx, &cmd, 50); err != nil {
		log.Println(err)
		return nil, toStatus(err)
	}

	if err := m.administrator.SendRequest(&oortapi.CreateInheritanceRelReq{
		From: &oortapi.Resource{Id: nsId, Kind: "namespace"},
		To:   &oortapi.Resource{Id: app.GetId(), Kind: "app"},
	}, func(resp *oortapi.AdministrationAsyncResp) {
		log.Println(resp.Error)
	}); err != nil {
		log.Println(err)
	}
	return &api.AddAppResp{}, nil
}


func (m MeridianGrpcHandler) RemoveApp(ctx context.Context, req *api.RemoveAppReq) (*api.RemoveAppResp, error) {
	nsId := domain.MakeNamespaceId(req.OrgId, req.Namespace)
	appId := domain.MakeAppId(req.OrgId, req.Namespace, req.Name)
	perms := []perm{
		{"org.namespace.get", "namespace", nsId},
		{"app.delete", "app", appId},
	}

	successorId := ""
	if req.SuccessorApp != "" {
		successorNs := req.SuccessorNamespace
		if successorNs == "" {
			successorNs = req.Namespace
		}
		successorId = domain.MakeAppId(req.OrgId, successorNs, req.SuccessorApp)

		perms = append(perms,
			perm{"org.namespace.get", "namespace", domain.MakeNamespaceId(req.OrgId, successorNs)},
			perm{"app.put", "app", successorId},
		)
	}
	if err := m.authorize(ctx, perms...); err != nil {
		return nil, err
	}

	if err := m.terminate.TerminateApp(ctx, appId, successorId); err != nil {
		log.Println("RemoveApp:", err)
		return nil, toStatus(err)
	}
	return &api.RemoveAppResp{}, nil
}
