package handlers

import (
	"github.com/c12s/meridian/internal/domain"
	"github.com/c12s/meridian/internal/nodes"
	"github.com/c12s/meridian/internal/services"
	"github.com/c12s/meridian/pkg/api"
	oortapi "github.com/c12s/oort/pkg/api"
	pulsar_api "github.com/c12s/pulsar/model/protobuf"
)


type MeridianGrpcHandler struct {
	api.UnimplementedMeridianServer
	namespaces    domain.NamespaceStore
	apps          domain.AppStore
	resources     domain.ResourceQuotaStore
	loans         domain.LoanStore
	pulsar        pulsar_api.SeccompServiceClient
	administrator *oortapi.AdministrationAsyncClient
	authorizer    services.AuthZService
	nodes         *nodes.Dispatcher
	borrow        *services.BorrowService
	terminate     *services.TerminationService
}

func NewMeridianGrpcHandler(
	namespaces domain.NamespaceStore,
	apps domain.AppStore,
	resources domain.ResourceQuotaStore,
	loans domain.LoanStore,
	pulsar pulsar_api.SeccompServiceClient,
	administrator *oortapi.AdministrationAsyncClient,
	authorizer services.AuthZService,
	nodes *nodes.Dispatcher,
	borrow *services.BorrowService,
	terminate *services.TerminationService,
) MeridianGrpcHandler {
	return MeridianGrpcHandler{
		namespaces:    namespaces,
		apps:          apps,
		resources:     resources,
		loans:         loans,
		pulsar:        pulsar,
		administrator: administrator,
		authorizer:    authorizer,
		nodes:         nodes,
		borrow:        borrow,
		terminate:     terminate,
	}
}
