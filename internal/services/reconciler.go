package services

import (
	"context"
	"log"
	"time"

	"github.com/c12s/meridian/internal/domain"
)

type AppSyncer interface {
	SyncApp(ctx context.Context, appId string) error
}

// periodically repushes each apps desired quotas to the nodes
type Reconciler struct {
	apps QuotaAccessor
	sync AppSyncer
}

func NewReconciler(apps QuotaAccessor, sync AppSyncer) *Reconciler {
	return &Reconciler{apps: apps, sync: sync}
}

func (r *Reconciler) Reconcile(ctx context.Context) error {
	ids, err := r.apps.ListApps()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !domain.IsAppId(id) {
			continue
		}
		if err := r.sync.SyncApp(ctx, id); err != nil {
			log.Printf("reconcile: sync %s failed: %v", id, err)
		}
	}
	return nil
}

// reconciles every interval until ctx is cancelled
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	runEvery(ctx, "reconcile", interval, func(ctx context.Context) {
		if err := r.Reconcile(ctx); err != nil {
			log.Printf("reconcile pass failed: %v", err)
		}
	})
}
