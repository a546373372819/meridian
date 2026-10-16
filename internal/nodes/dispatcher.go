// delivers meridians commands to the nodes of an org 

// it picks the nodes (magnetar), authorizes as meridian (mint service token)
// and sends an ApplyAppConfigCommand to each node through gravity, which
// forwards it over NATS to the nodes star agent
package nodes

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"

	gravityapi "github.com/c12s/gravity/pkg/api"
	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/c12s/meridian/internal/domain"
	"github.com/c12s/meridian/pkg/api"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// bounds the gravity call for a single node
const nodeDispatchTimeout = 5 * time.Second

// mints a token carrying the given permissions
type TokenMinter interface {
	MintToken(username string, permissions []string) (string, error)
}

// sends commands to an orgs nodes
type Dispatcher struct {
	magnetar magnetarapi.MagnetarClient
	gravity  gravityapi.AgentQueueClient
	tokens   TokenMinter
}

func NewDispatcher(magnetar magnetarapi.MagnetarClient, gravity gravityapi.AgentQueueClient, tokens TokenMinter) *Dispatcher {
	return &Dispatcher{magnetar: magnetar, gravity: gravity, tokens: tokens}
}

// pushes an apps quotas to every node of its org. A node notrunning the apps container does nothing
func (d *Dispatcher) DispatchQuotas(ctx context.Context, orgId, namespaceName, appName string, quotas domain.ResourceQuotas) error {
	return d.Disseminate(ctx, &api.ApplyAppConfigCommand{
		OrgId:         orgId,
		NamespaceName: namespaceName,
		AppName:       appName,
		Quotas:        quotas,
	}, 100)
}

// tells every node of the org to remove the apps container
func (d *Dispatcher) TerminateContainers(ctx context.Context, orgId, namespaceName, appName string) error {
	return d.Disseminate(ctx, &api.ApplyAppConfigCommand{
		OrgId:         orgId,
		NamespaceName: namespaceName,
		AppName:       appName,
		Terminate:     true,
	}, 100)
}

// picks percentage of the orgs nodes and sends cmd (seccomp profile, resource quotas and/or a terminate request) to each of them
func (d *Dispatcher) Disseminate(ctx context.Context, cmd *api.ApplyAppConfigCommand, percentage int32) error {
	nodes, err := d.place(d.serviceContext(ctx, cmd.OrgId, nil), cmd.OrgId, percentage)
	if err != nil {
		return err
	}
	cmdMarshalled, err := proto.Marshal(cmd)
	if err != nil {
		return err
	}
	nodePerms := make([]string, 0, len(nodes))
	for _, node := range nodes {
		nodePerms = append(nodePerms, "config.app.send|node|"+node.Id)
	}
	sendCtx := d.serviceContext(ctx, cmd.OrgId, nodePerms)

	var errs []error
	for _, node := range nodes {
		nodeCtx, cancel := context.WithTimeout(sendCtx, nodeDispatchTimeout)
		_, err := d.gravity.DisseminateAppConfig(nodeCtx, &gravityapi.DeseminateConfigRequest{
			NodeId: node.Id,
			Config: cmdMarshalled,
		})
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", node.Id, err))
		}
	}
	return errors.Join(errs...)
}


func (d *Dispatcher) serviceContext(ctx context.Context, org string, extra []string) context.Context {
	perms := append([]string{"node.get|org|" + org}, extra...)
	tok, err := d.tokens.MintToken("meridian-service", perms)
	if err != nil {
		log.Println("dispatch: mint service token:", err)
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			return metadata.NewOutgoingContext(ctx, md)
		}
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authz-token", tok))
}

func (d *Dispatcher) place(ctx context.Context, org string, percentage int32) ([]*magnetarapi.NodeStringified, error) {
	resp, err := d.magnetar.ListOrgOwnedNodes(ctx, &magnetarapi.ListOrgOwnedNodesReq{Org: org})
	if err != nil {
		return nil, err
	}
	return selectRandomNodes(resp.Nodes, percentage), nil
}

func selectRandomNodes(nodes []*magnetarapi.NodeStringified, percentage int32) []*magnetarapi.NodeStringified {
	n := int(math.Ceil(float64(len(nodes)) * float64(percentage) / 100))
	if n > len(nodes) {
		n = len(nodes)
	}
	selected := make([]*magnetarapi.NodeStringified, 0, n)
	for _, i := range rand.Perm(len(nodes))[:n] {
		selected = append(selected, nodes[i])
	}
	return selected
}
