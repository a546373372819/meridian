package nodes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	gravityapi "github.com/c12s/gravity/pkg/api"
	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/c12s/meridian/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// embeds the client interface and overrides only the one method the dispatcher needs
type fakeMagnetar struct {
	magnetarapi.MagnetarClient
	nodes []*magnetarapi.NodeStringified
}

func (f fakeMagnetar) ListOrgOwnedNodes(ctx context.Context, in *magnetarapi.ListOrgOwnedNodesReq, opts ...grpc.CallOption) (*magnetarapi.ListOrgOwnedNodesResp, error) {
	return &magnetarapi.ListOrgOwnedNodesResp{Nodes: f.nodes}, nil
}

// captures every DisseminateAppConfig call and the token it carried.
type fakeGravity struct {
	gravityapi.AgentQueueClient
	mu     sync.Mutex
	reqs   []*gravityapi.DeseminateConfigRequest
	tokens []string
	fail   map[string]bool // node ids whose dispatch fails
}

func (f *fakeGravity) DisseminateAppConfig(ctx context.Context, in *gravityapi.DeseminateConfigRequest, opts ...grpc.CallOption) (*gravityapi.DeseminateConfigResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, in)
	md, _ := metadata.FromOutgoingContext(ctx)
	f.tokens = append(f.tokens, md.Get("authz-token")...)
	if f.fail[in.NodeId] {
		return nil, errors.New("node unreachable")
	}
	return &gravityapi.DeseminateConfigResponse{}, nil
}

type fakeTokens struct{}

func (fakeTokens) MintToken(string, []string) (string, error) { return "service-token", nil }

func TestDisseminateReachesEveryNodeAsMeridian(t *testing.T) {
	mag := fakeMagnetar{nodes: []*magnetarapi.NodeStringified{{Id: "node-a"}, {Id: "node-b"}}}
	grav := &fakeGravity{}
	d := NewDispatcher(mag, grav, fakeTokens{})
	// a callers own token must not be what reaches the nodes
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authz-token", "user-token"))

	if err := d.DispatchQuotas(ctx, "org1", "ns1", "web", map[string]float64{"cpu": 2, "mem": 512}); err != nil {
		t.Fatalf("DispatchQuotas: %v", err)
	}
	if len(grav.reqs) != 2 {
		t.Fatalf("dispatched %d times, want 2 (one per node)", len(grav.reqs))
	}
	seen := map[string]bool{}
	for _, r := range grav.reqs {
		seen[r.NodeId] = true
		var got api.ApplyAppConfigCommand
		if err := proto.Unmarshal(r.Config, &got); err != nil {
			t.Fatalf("unmarshal dispatched config: %v", err)
		}
		if got.AppName != "web" || got.Quotas["cpu"] != 2 || got.Quotas["mem"] != 512 {
			t.Errorf("dispatched %v, want app web with cpu:2 mem:512", &got)
		}
	}
	if !seen["node-a"] || !seen["node-b"] {
		t.Errorf("did not reach both nodes, saw: %v", seen)
	}
	for _, tok := range grav.tokens {
		if tok != "service-token" {
			t.Errorf("node got token %q, want meridian's service token", tok)
		}
	}
}

// one unreachable node must not stop the others from getting the command, the failure is still reported
func TestDisseminateContinuesPastFailedNode(t *testing.T) {
	nodes := []*magnetarapi.NodeStringified{{Id: "node-a"}, {Id: "node-b"}, {Id: "node-c"}}
	grav := &fakeGravity{fail: map[string]bool{"node-a": true, "node-b": true}}
	d := NewDispatcher(fakeMagnetar{nodes: nodes}, grav, fakeTokens{})

	if err := d.TerminateContainers(context.Background(), "org1", "ns1", "web"); err == nil {
		t.Fatal("expected the failed nodes to be reported")
	}
	if len(grav.reqs) != 3 {
		t.Fatalf("attempted %d nodes, want all 3", len(grav.reqs))
	}
}

func TestSelectRandomNodes(t *testing.T) {
	nodes := []*magnetarapi.NodeStringified{{Id: "a"}, {Id: "b"}, {Id: "c"}, {Id: "d"}}
	before := fmt.Sprint(nodes)

	half := selectRandomNodes(nodes, 50)
	if len(half) != 2 || half[0].Id == half[1].Id {
		t.Errorf("50%% of 4 nodes = %v, want 2 distinct nodes", half)
	}
	if all := selectRandomNodes(nodes, 100); len(all) != 4 {
		t.Errorf("100%% of 4 nodes gave %d", len(all))
	}
	if fmt.Sprint(nodes) != before {
		t.Error("selectRandomNodes must not modify its input")
	}
}
