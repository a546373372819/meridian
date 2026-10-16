package services

import (
	"context"
	"testing"

	"github.com/c12s/meridian/internal/domain"
)

// reconciler sends every app's current quotas and skips ids that are not apps
func TestReconcileSendsCurrentQuotasOfEveryApp(t *testing.T) {
	q := newFakeQuotas()
	q.set("org1/ns1/web", domain.ResourceQuotas{"cpu": 3})
	q.set("org1/ns2/db", domain.ResourceQuotas{"cpu": 1})
	q.set("malformed", domain.ResourceQuotas{"cpu": 1})
	disp := &fakeDispatcher{}
	r := NewReconciler(q, newTestBorrowService(q, newFakeLoans(), disp))

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	sent := map[string]float64{}
	for _, c := range disp.calls {
		sent[c.org+"/"+c.ns+"/"+c.app] = c.quotas["cpu"]
	}
	if len(sent) != 2 || sent["org1/ns1/web"] != 3 || sent["org1/ns2/db"] != 1 {
		t.Errorf("sent %v, want web=3 and db=1 (malformed skipped)", sent)
	}
}
