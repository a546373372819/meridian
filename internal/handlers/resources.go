package handlers

import (
	"context"
	"log"

	"github.com/c12s/meridian/internal/domain"
	"github.com/c12s/meridian/pkg/api"
)

func (m MeridianGrpcHandler) SetNamespaceResources(ctx context.Context, req *api.SetNamespaceResourcesReq) (*api.SetNamespaceResourcesResp, error) {
	nsId := domain.MakeNamespaceId(req.OrgId, req.Name)
	if err := m.authorize(ctx, perm{"namespace.put", "namespace", nsId}); err != nil {
		return nil, err
	}
	if err := m.resources.SetResourceQuotas(nsId, domain.ResourceQuotas(req.Quotas), nil); err != nil {
		log.Println(err)
		return nil, toStatus(err)
	}
	return &api.SetNamespaceResourcesResp{}, nil
}

func (m MeridianGrpcHandler) SetAppResources(ctx context.Context, req *api.SetAppResourcesReq) (*api.SetAppResourcesResp, error) {
	nsId := domain.MakeNamespaceId(req.OrgId, req.Namespace)
	appId := domain.MakeAppId(req.OrgId, req.Namespace, req.Name)
	// checks the namespace belongs to the calle's org
	if err := m.authorize(ctx,
		perm{"org.namespace.get", "namespace", nsId},
		perm{"app.put", "app", appId},
	); err != nil {
		return nil, err
	}
	if err := m.resources.SetResourceQuotas(appId, domain.ResourceQuotas(req.Quotas), nil); err != nil {
		log.Println(err)
		return nil, toStatus(err)
	}

	if err := m.nodes.Disseminate(ctx, &api.ApplyAppConfigCommand{
		OrgId:         req.OrgId,
		NamespaceName: req.Namespace,
		AppName:       req.Name,
		Quotas:        req.Quotas,
	}, 100); err != nil {
		log.Println("SetAppResources: quota dispatch failed:", err)
	}
	return &api.SetAppResourcesResp{}, nil
}


func transferPerms(org, fromNs, fromApp, toNs, toApp string) (fromId, toId string, perms []perm) {
	fromId = domain.MakeAppId(org, fromNs, fromApp)
	toId = domain.MakeAppId(org, toNs, toApp)
	return fromId, toId, []perm{
		{"org.namespace.get", "namespace", domain.MakeNamespaceId(org, fromNs)},
		{"org.namespace.get", "namespace", domain.MakeNamespaceId(org, toNs)},
		{"app.put", "app", fromId},
		{"app.put", "app", toId},
	}
}


func (m MeridianGrpcHandler) BorrowResources(ctx context.Context, req *api.BorrowResourcesReq) (*api.BorrowResourcesResp, error) {
	fromId, toId, perms := transferPerms(req.OrgId, req.FromNamespace, req.FromApp, req.ToNamespace, req.ToApp)
	if err := m.authorize(ctx, perms...); err != nil {
		return nil, err
	}
	if err := m.borrow.Borrow(ctx, fromId, toId, req.Resource, req.Amount); err != nil {
		log.Println("BorrowResources failed:", err)
		return nil, toStatus(err)
	}
	return &api.BorrowResourcesResp{}, nil
}

func (m MeridianGrpcHandler) ReturnResources(ctx context.Context, req *api.ReturnResourcesReq) (*api.ReturnResourcesResp, error) {
	fromId, toId, perms := transferPerms(req.OrgId, req.FromNamespace, req.FromApp, req.ToNamespace, req.ToApp)
	if err := m.authorize(ctx, perms...); err != nil {
		return nil, err
	}
	if err := m.borrow.Return(ctx, fromId, toId, req.Resource, req.Amount); err != nil {
		log.Println("ReturnResources failed:", err)
		return nil, toStatus(err)
	}
	return &api.ReturnResourcesResp{}, nil
}
