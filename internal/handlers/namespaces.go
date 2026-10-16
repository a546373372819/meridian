package handlers

import (
	"context"
	"log"

	"github.com/c12s/meridian/internal/domain"
	"github.com/c12s/meridian/pkg/api"
	oortapi "github.com/c12s/oort/pkg/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (m MeridianGrpcHandler) AddNamespace(ctx context.Context, req *api.AddNamespaceReq) (*api.AddNamespaceResp, error) {
	if err := m.authorize(ctx, perm{"org.namespace.add", "org", req.OrgId}); err != nil {
		return nil, err
	}

	if _, err := m.namespaces.Get(domain.MakeNamespaceId(req.OrgId, req.Name)); err == nil {
		return nil, status.Error(codes.AlreadyExists, "namespace already exists")
	}
	var parent *domain.Namespace
	if req.ParentName != "" {
		p, err := m.namespaces.Get(domain.MakeNamespaceId(req.OrgId, req.ParentName))
		if err != nil {
			log.Println(err)
			return nil, status.Error(codes.NotFound, "parent namespace not found")
		}
		parent = &p
	}
	namespace := domain.NewNamespace(req.OrgId, req.Name, req.Profile.Version, req.Labels)
	for resource, quota := range req.Quotas {
		if err := namespace.AddResourceQuota(resource, quota); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	if err := m.sendSeccompProfile(ctx, req.SeccompDefinitionStrategy, namespace.GetSeccompProfile(), req.Profile, parent); err != nil {
		return nil, toStatus(err)
	}
	if err := m.namespaces.Add(namespace, parent); err != nil {
		log.Println(err)
		return nil, toStatus(err)
	}

	parentRes := &oortapi.Resource{Id: req.OrgId, Kind: "org"}
	if parent != nil {
		parentRes = &oortapi.Resource{Id: parent.GetId(), Kind: "namespace"}
	}
	if err := m.administrator.SendRequest(&oortapi.CreateInheritanceRelReq{
		From: parentRes,
		To:   &oortapi.Resource{Id: namespace.GetId(), Kind: "namespace"},
	}, func(resp *oortapi.AdministrationAsyncResp) {
		log.Println(resp.Error)
	}); err != nil {
		log.Println(err)
	}
	return &api.AddNamespaceResp{}, nil
}

func (m MeridianGrpcHandler) RemoveNamespace(ctx context.Context, req *api.RemoveNamespaceReq) (*api.RemoveNamespaceResp, error) {
	nsId := domain.MakeNamespaceId(req.OrgId, req.Name)
	if err := m.authorize(ctx, perm{"namespace.delete", "namespace", nsId}); err != nil {
		return nil, err
	}
	tree, err := m.namespaces.GetHierarchy(nsId)
	if err == nil && (len(tree.Root.Children) > 0 || len(tree.Root.Apps) > 0) {
		return nil, status.Error(codes.FailedPrecondition, "namespace must not have applications or child namespaces")
	}

	loans, err := m.loans.Involving(nsId)
	if err != nil {
		log.Println(err)
		return nil, toStatus(err)
	}
	if len(loans) > 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "namespace %s has %d outstanding loan(s); return them before removing it", nsId, len(loans))
	}
	if err := m.namespaces.Remove(nsId); err != nil {
		log.Println(err)
		return nil, toStatus(err)
	}
	return &api.RemoveNamespaceResp{}, nil
}

func (m MeridianGrpcHandler) GetNamespace(ctx context.Context, req *api.GetNamespaceReq) (*api.GetNamespaceResp, error) {
	nsId := domain.MakeNamespaceId(req.OrgId, req.Name)

	namespace, err := m.namespaces.Get(nsId)
	if err != nil {
		log.Println(err)
		return nil, status.Error(codes.NotFound, "namespace not found")
	}
	if err := m.authorize(ctx, perm{"org.namespace.get", "namespace", nsId}); err != nil {
		return nil, err
	}
	return &api.GetNamespaceResp{
		Name:      namespace.GetName(),
		Labels:    namespace.GetLabels(),
		Total:     namespace.GetResourceQuotas(),
		Available: namespace.GetAvailable(),
		Utilized:  namespace.GetUtilized(),
		Profile:   m.getSeccompProfile(ctx, namespace.GetSeccompProfile()),
	}, nil
}

func (m MeridianGrpcHandler) GetNamespaceHierarchy(ctx context.Context, req *api.GetNamespaceHierarchyReq) (*api.GetNamespaceHierarchyResp, error) {
	nsId := domain.MakeNamespaceId(req.OrgId, "default")
	if err := m.authorize(ctx, perm{"org.namespace.get", "namespace", nsId}); err != nil {
		return nil, err
	}
	tree, err := m.namespaces.GetHierarchy(nsId)
	if err != nil {
		log.Println(err)
		return nil, status.Error(codes.NotFound, "namespace hierarchy not found")
	}
	return m.mapNamespaceTreeNode(ctx, &tree.Root), nil
}

func (m MeridianGrpcHandler) mapNamespaceTreeNode(ctx context.Context, node *domain.NamespaceTreeNode) *api.GetNamespaceHierarchyResp {
	resp := &api.GetNamespaceHierarchyResp{
		Namespace: &api.GetNamespaceHierarchyResp_Namespace{
			Name:      node.Namespace.GetName(),
			Labels:    node.Namespace.GetLabels(),
			Total:     node.Namespace.GetResourceQuotas(),
			Available: node.Namespace.GetAvailable(),
			Utilized:  node.Namespace.GetUtilized(),
			Profile:   m.getSeccompProfile(ctx, node.Namespace.GetSeccompProfile()),
		},
	}
	for _, app := range node.Apps {
		resp.Apps = append(resp.Apps, &api.GetNamespaceHierarchyResp_App{
			Name:    app.GetName(),
			Total:   app.GetResourceQuotas(),
			Profile: m.getSeccompProfile(ctx, app.GetSeccompProfile()),
		})
	}
	for _, child := range node.Children {
		resp.Namespaces = append(resp.Namespaces, m.mapNamespaceTreeNode(ctx, child))
	}
	return resp
}
