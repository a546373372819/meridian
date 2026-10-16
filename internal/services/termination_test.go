package services

import (
	"context"
	"testing"

	"github.com/c12s/meridian/internal/domain"
)

// removes an app by deleting its quotas from the fake quota store
type fakeApps struct {
	quotas  *fakeQuotas
	removed []string
}

func (f *fakeApps) Remove(id string) error {
	delete(f.quotas.m, id)
	f.removed = append(f.removed, id)
	return nil
}

type fakeTerminator struct{ terminated []string }

func (f *fakeTerminator) TerminateContainers(_ context.Context, org, ns, app string) error {
	f.terminated = append(f.terminated, org+"/"+ns+"/"+app)
	return nil
}

func newTestTermination(q *fakeQuotas, loans *fakeLoans) (*TerminationService, *fakeApps, *fakeTerminator) {
	apps := &fakeApps{quotas: q}
	nodes := &fakeTerminator{}
	return NewTerminationService(q, apps, newTestBorrowService(q, loans, &fakeDispatcher{}), nodes), apps, nodes
}

func TestTerminateRejectsMissingOrSelfSuccessor(t *testing.T) {
	q := newFakeQuotas()
	q.set(appWeb, domain.ResourceQuotas{"cpu": 2})
	ts, apps, _ := newTestTermination(q, newFakeLoans())
	ctx := context.Background()

	if err := ts.TerminateApp(ctx, appWeb, "org1/ns1/missing"); domain.KindOf(err) != domain.KindNotFound {
		t.Errorf("missing successor: got %v, want a not-found error", err)
	}
	if err := ts.TerminateApp(ctx, appWeb, appWeb); domain.KindOf(err) != domain.KindInvalid {
		t.Errorf("own successor: got %v, want an invalid-argument error", err)
	}
	if err := ts.TerminateApp(ctx, appWeb, appOther); domain.KindOf(err) != domain.KindInvalid {
		t.Errorf("successor in another org: got %v, want an invalid-argument error", err)
	}
	if len(apps.removed) != 0 {
		t.Errorf("rejected terminations removed %v", apps.removed)
	}
}

// same namespace - the successor takes every loan and the whole quota
func TestTerminateSameNamespaceSuccessor(t *testing.T) {
	const web2 = "org1/ns1/web2"
	q := newFakeQuotas()
	q.set(appAPI, domain.ResourceQuotas{"cpu": 2}) // lent 2 to web
	q.set(appWeb, domain.ResourceQuotas{"cpu": 3}) // own 1 + borrowed 2
	q.set(web2, domain.ResourceQuotas{"cpu": 1})
	loans := newFakeLoans()
	loans.record(appAPI, appWeb, "cpu", 2, false)
	ts, apps, nodes := newTestTermination(q, loans)

	if err := ts.TerminateApp(context.Background(), appWeb, web2); err != nil {
		t.Fatalf("TerminateApp: %v", err)
	}
	if out, _ := loans.Outstanding(appAPI, web2, "cpu", false); out != 2 {
		t.Errorf("web2 should now owe api 2, got %g", out)
	}
	if got, _ := q.GetQuotas(web2); got["cpu"] != 4 {
		t.Errorf("web2 cpu = %g, want 4 (1 + web's 3)", got["cpu"])
	}
	if len(apps.removed) != 1 || apps.removed[0] != appWeb || len(nodes.terminated) != 1 {
		t.Errorf("web not removed/torn down: removed=%v terminated=%v", apps.removed, nodes.terminated)
	}
}

// other namespace
func TestTerminateCrossNamespaceSuccessor(t *testing.T) {
	q := newFakeQuotas()
	q.free["org1/ns1"] = domain.ResourceQuotas{"cpu": 1} 
	q.set(appAPI, domain.ResourceQuotas{"cpu": 2})
	q.set(appWeb, domain.ResourceQuotas{"cpu": 4}) 
	q.set(appDB, domain.ResourceQuotas{"cpu": 1})  
	loans := newFakeLoans()
	loans.record(appAPI, appWeb, "cpu", 2, false)
	loans.record("org1/ns1", appWeb, "cpu", 1, true)
	ts, _, _ := newTestTermination(q, loans)

	if err := ts.TerminateApp(context.Background(), appWeb, appDB); err != nil {
		t.Fatalf("TerminateApp: %v", err)
	}
	if got, _ := q.GetQuotas(appAPI); got["cpu"] != 4 {
		t.Errorf("api should get its 2 back, cpu = %g", got["cpu"])
	}
	if got, _ := q.GetQuotas(appDB); got["cpu"] != 2 {
		t.Errorf("db cpu = %g, want 2 (1 + web's own 1)", got["cpu"])
	}
	if out, _ := loans.Outstanding("org1/ns1", appDB, "cpu", false); out != 1 {
		t.Errorf("db should owe web's namespace 1, got %g", out)
	}
	if l, _ := loans.Involving(appWeb); len(l) != 0 {
		t.Errorf("web's loans should all be settled, got %+v", l)
	}
}

// terminating a lender must not shrink a possibly busy borrower, the loan is repointed onto the lende's namespace
func TestTerminateLenderRepointsLoanToNamespace(t *testing.T) {
	q := newFakeQuotas()
	q.set(appAPI, domain.ResourceQuotas{"cpu": 2}) // api: own 4, lent 2 -> holds 2
	q.set(appWeb, domain.ResourceQuotas{"cpu": 3}) // web: own 1 + borrowed 2 from api
	loans := newFakeLoans()
	loans.record(appAPI, appWeb, "cpu", 2, false) // web owes api 2 (api is the lender)
	ts, apps, _ := newTestTermination(q, loans)

	if err := ts.TerminateApp(context.Background(), appAPI, ""); err != nil {
		t.Fatalf("TerminateApp(api): %v", err)
	}
	// the borrower is NOT shrunk
	if got, _ := q.GetQuotas(appWeb); got["cpu"] != 3 {
		t.Errorf("web cpu = %g, want 3 (unchanged — not reclaimed from a busy app)", got["cpu"])
	}
	// the old app->app loan is gone
	if out, _ := loans.Outstanding(appAPI, appWeb, "cpu", false); out != 0 {
		t.Errorf("old loan web->api should be cleared, got %g", out)
	}
	// it is re-pointed onto api's namespace as an elastic loan
	if out, _ := loans.Outstanding("org1/ns1", appWeb, "cpu", true); out != 2 {
		t.Errorf("web should owe its namespace 2 (elastic), got %g", out)
	}
	if len(apps.removed) != 1 || apps.removed[0] != appAPI {
		t.Errorf("api not removed: %v", apps.removed)
	}
}

// if a loan cannot be settled the app stays, so the loan is not orphaned.
func TestTerminateKeepsAppWhenLoansCannotBeSettled(t *testing.T) {
	q := newFakeQuotas()
	q.set(appAPI, domain.ResourceQuotas{"cpu": 2})
	q.set(appWeb, domain.ResourceQuotas{"cpu": 1}) 
	loans := newFakeLoans()
	loans.record(appAPI, appWeb, "cpu", 2, false)
	ts, apps, nodes := newTestTermination(q, loans)

	err := ts.TerminateApp(context.Background(), appWeb, "")
	if domain.KindOf(err) != domain.KindPrecondition {
		t.Fatalf("got %v, want a failed-precondition error", err)
	}
	if len(apps.removed) != 0 || len(nodes.terminated) != 0 {
		t.Errorf("app must stay: removed=%v terminated=%v", apps.removed, nodes.terminated)
	}
}
