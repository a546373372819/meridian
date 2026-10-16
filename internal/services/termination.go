package services

import (
	"context"
	"log"

	"github.com/c12s/meridian/internal/domain"
)

// deletes an app from meridians graph
type AppRemover interface {
	Remove(id string) error
}

// tells the nodes to remove an apps container.
type ContainerTerminator interface {
	TerminateContainers(ctx context.Context, orgId, namespaceName, appName string) error
}

// removes an app and, if it has a successor, hands the successor what the app held.
type TerminationService struct {
	quotas QuotaAccessor
	apps   AppRemover
	borrow *BorrowService
	nodes  ContainerTerminator
}

func NewTerminationService(quotas QuotaAccessor, apps AppRemover, borrow *BorrowService, nodes ContainerTerminator) *TerminationService {
	return &TerminationService{quotas: quotas, apps: apps, borrow: borrow, nodes: nodes}
}

// removes appId. successorId is "" for no successor, or the app that takes over

func (t *TerminationService) TerminateApp(ctx context.Context, appId, successorId string) error {
	if !domain.IsAppId(appId) {
		return domain.Invalid("%q is not an app id", appId)
	}
	hasSuccessor := successorId != ""
	if hasSuccessor {
		if successorId == appId {
			return domain.Invalid("an app cannot be its own successor")
		}
		if !domain.IsAppId(successorId) || domain.OrgOf(successorId) != domain.OrgOf(appId) {
			return domain.Invalid("successor %q must be an app in the same organization", successorId)
		}
		// Check BEFORE touching any loan: handing loans to a missing app would
		// lose them when the app is deleted.
		if _, err := t.quotas.GetQuotas(successorId); err != nil {
			return domain.NotFound("successor app %s not found", successorId)
		}
	}
	sameNamespace := hasSuccessor && domain.NamespaceOf(successorId) == domain.NamespaceOf(appId)

	if sameNamespace {
		if err := t.borrow.ReassignLoansToSuccessor(ctx, appId, successorId); err != nil {
			return domain.Precondition("could not hand loans to successor, app not removed: %w", err)
		}
	} else {
		if err := t.borrow.SettleLoans(ctx, appId); err != nil {
			return domain.Precondition("could not settle the app's loans, app not removed: %w", err)
		}
	}

	var inherited domain.ResourceQuotas
	if hasSuccessor {
		q, err := t.quotas.GetQuotas(appId)
		if err != nil {
			log.Printf("terminate %s: reading its resources: %v", appId, err)
		}
		inherited = q
	}

	if err := t.apps.Remove(appId); err != nil {
		return err
	}

	if hasSuccessor && len(inherited) > 0 {
		if sameNamespace {
			if err := t.borrow.InheritResources(ctx, successorId, inherited); err != nil {
				log.Printf("terminate %s: successor inheritance failed: %v", appId, err)
			}
		} else if err := t.borrow.BorrowFromNamespace(ctx, domain.NamespaceOf(appId), successorId, inherited); err != nil {
			log.Printf("terminate %s: cross-namespace successor borrow failed: %v", appId, err)
		}
	}

	org, ns, app, _ := domain.SplitAppId(appId)
	if err := t.nodes.TerminateContainers(ctx, org, ns, app); err != nil {
		log.Printf("terminate %s: container teardown failed: %v", appId, err)
	}
	return nil
}
