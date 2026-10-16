package services

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/c12s/meridian/internal/domain"
)

const (
	appAPI   = "org1/ns1/api" // lender
	appWeb   = "org1/ns1/web" // borrower (same namespace)
	appDB    = "org1/ns2/db"  // borrower in a different namespace, same org
	appOther = "org2/ns1/x"   // app in a different organization
)

// borrow across namespaces moves only the borrowed resource,n records the loan, resizes both containers, returning it restores everything
func TestBorrowAndReturnAcrossNamespaces(t *testing.T) {
	q := newFakeQuotas()
	q.set(appAPI, domain.ResourceQuotas{"cpu": 4, "mem": 1024})
	q.set(appDB, domain.ResourceQuotas{"cpu": 1, "mem": 512})
	loans := newFakeLoans()
	disp := &fakeDispatcher{}
	s := newTestBorrowService(q, loans, disp)
	ctx := context.Background()

	if err := s.Borrow(ctx, appAPI, appDB, "cpu", 2); err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	api, _ := q.GetQuotas(appAPI)
	db, _ := q.GetQuotas(appDB)
	if api["cpu"] != 2 || db["cpu"] != 3 || api["mem"] != 1024 || db["mem"] != 512 {
		t.Errorf("after borrow api=%v db=%v, want cpu 2/3 and mem untouched", api, db)
	}
	if out, _ := loans.Outstanding(appAPI, appDB, "cpu", false); out != 2 {
		t.Errorf("loan = %g, want 2", out)
	}
	if len(disp.calls) != 2 {
		t.Errorf("dispatched %d times, want 2 (lender + borrower)", len(disp.calls))
	}

	if err := s.Return(ctx, appAPI, appDB, "cpu", 2); err != nil {
		t.Fatalf("Return: %v", err)
	}
	api, _ = q.GetQuotas(appAPI)
	db, _ = q.GetQuotas(appDB)
	if api["cpu"] != 4 || db["cpu"] != 1 {
		t.Errorf("after return api cpu=%g db cpu=%g, want 4/1", api["cpu"], db["cpu"])
	}
	if out, _ := loans.Outstanding(appAPI, appDB, "cpu", false); out != 0 {
		t.Errorf("loan after full return = %g, want 0", out)
	}
}

// every invalid request is rejected before anything is written or dispatched.
func TestBorrowRejectsInvalidRequests(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	cases := []struct {
		name string
		call func(s *BorrowService, ctx context.Context) error
	}{
		{"across organizations", func(s *BorrowService, ctx context.Context) error {
			return s.Borrow(ctx, appAPI, appOther, "cpu", 1)
		}},
		{"lender holds too little", func(s *BorrowService, ctx context.Context) error {
			return s.Borrow(ctx, appAPI, appWeb, "cpu", 5)
		}},
		{"from itself", func(s *BorrowService, ctx context.Context) error {
			return s.Borrow(ctx, appAPI, appAPI, "cpu", 1)
		}},
		{"unsupported resource", func(s *BorrowService, ctx context.Context) error {
			return s.Borrow(ctx, appAPI, appWeb, "gpu", 1)
		}},
		// NaN slips past "<= 0" and "holds enough" (every NaN comparison is
		// false), so it must be rejected explicitly.
		{"NaN amount", func(s *BorrowService, ctx context.Context) error {
			return s.Borrow(ctx, appAPI, appWeb, "cpu", nan)
		}},
		{"infinite amount", func(s *BorrowService, ctx context.Context) error {
			return s.Borrow(ctx, appAPI, appWeb, "cpu", inf)
		}},
		{"return more than owed", func(s *BorrowService, ctx context.Context) error {
			return s.Return(ctx, appAPI, appWeb, "cpu", 1)
		}},
		{"return NaN", func(s *BorrowService, ctx context.Context) error {
			return s.Return(ctx, appAPI, appWeb, "cpu", nan)
		}},
		{"namespace borrow across organizations", func(s *BorrowService, ctx context.Context) error {
			return s.BorrowFromNamespace(ctx, "org1/ns1", appOther, domain.ResourceQuotas{"cpu": 1})
		}},
		{"namespace borrow NaN", func(s *BorrowService, ctx context.Context) error {
			return s.BorrowFromNamespace(ctx, "org1/ns1", appWeb, domain.ResourceQuotas{"cpu": nan})
		}},
		{"pool borrow NaN", func(s *BorrowService, ctx context.Context) error {
			return s.BorrowFromPool(ctx, "org1/ns1", appWeb, domain.ResourceQuotas{"cpu": nan})
		}},
		{"pool return NaN", func(s *BorrowService, ctx context.Context) error {
			return s.ReturnToNamespace(ctx, "org1/ns1", appWeb, "cpu", nan, true)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := newFakeQuotas()
			q.set(appAPI, domain.ResourceQuotas{"cpu": 4})
			q.set(appWeb, domain.ResourceQuotas{"cpu": 1})
			q.set(appOther, domain.ResourceQuotas{"cpu": 1})
			q.free["org1/ns1"] = domain.ResourceQuotas{"cpu": 10}
			loans := newFakeLoans()
			disp := &fakeDispatcher{}
			s := newTestBorrowService(q, loans, disp)

			if err := c.call(s, context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			api, _ := q.GetQuotas(appAPI)
			web, _ := q.GetQuotas(appWeb)
			if api["cpu"] != 4 || web["cpu"] != 1 || len(loans.m) != 0 || len(disp.calls) != 0 {
				t.Errorf("rejected call changed state: api=%g web=%g loans=%v dispatches=%d",
					api["cpu"], web["cpu"], loans.m, len(disp.calls))
			}
		})
	}
}

// borrower must not relend what it borrowed
func TestBorrowRejectsRelendingBorrowed(t *testing.T) {
	q := newFakeQuotas()
	q.set(appAPI, domain.ResourceQuotas{"cpu": 4})
	q.set(appWeb, domain.ResourceQuotas{"cpu": 1})
	q.set(appDB, domain.ResourceQuotas{"cpu": 0})
	s := newTestBorrowService(q, newFakeLoans(), &fakeDispatcher{})
	ctx := context.Background()

	if err := s.Borrow(ctx, appAPI, appWeb, "cpu", 2); err != nil {
		t.Fatalf("Borrow api->web: %v", err)
	}
	if err := s.Borrow(ctx, appWeb, appDB, "cpu", 3); err == nil {
		t.Fatal("web re-lending borrowed cpu should be rejected")
	}
	if err := s.Borrow(ctx, appWeb, appDB, "cpu", 1); err != nil {
		t.Fatalf("web lending its own cpu should succeed: %v", err)
	}
	if err := s.Return(ctx, appAPI, appWeb, "cpu", 2); err != nil {
		t.Fatalf("api reclaiming its loan should succeed: %v", err)
	}
	api, _ := q.GetQuotas(appAPI)
	web, _ := q.GetQuotas(appWeb)
	db, _ := q.GetQuotas(appDB)
	if api["cpu"] != 4 || web["cpu"] != 0 || db["cpu"] != 1 {
		t.Errorf("final cpu api=%g web=%g db=%g, want 4/0/1", api["cpu"], web["cpu"], db["cpu"])
	}
}

// elastic and structural loans between the same pair stay separate and each return path only touches its own kind
func TestLoanKindsStaySeparate(t *testing.T) {
	q := newFakeQuotas()
	q.set(appAPI, domain.ResourceQuotas{"cpu": 4})
	q.set(appWeb, domain.ResourceQuotas{"cpu": 1})
	q.set(appDB, domain.ResourceQuotas{"cpu": 1})
	q.free["org1/ns1"] = domain.ResourceQuotas{"cpu": 5}
	loans := newFakeLoans()
	s := newTestBorrowService(q, loans, &fakeDispatcher{})
	ctx := context.Background()

	// namespace pool
	if err := s.BorrowFromPool(ctx, "org1/ns1", appDB, domain.ResourceQuotas{"cpu": 2}); err != nil {
		t.Fatalf("BorrowFromPool: %v", err)
	}
	if err := s.BorrowFromNamespace(ctx, "org1/ns1", appDB, domain.ResourceQuotas{"cpu": 3}); err != nil {
		t.Fatalf("BorrowFromNamespace: %v", err)
	}
	if err := s.ReturnToNamespace(ctx, "org1/ns1", appDB, "cpu", 2, true); err != nil {
		t.Fatalf("ReturnToNamespace elastic: %v", err)
	}
	if out, _ := loans.Outstanding("org1/ns1", appDB, "cpu", false); out != 3 {
		t.Errorf("structural namespace loan = %g, want 3 (untouched)", out)
	}
	if err := s.ReturnToNamespace(ctx, "org1/ns1", appDB, "cpu", 1, true); err == nil {
		t.Error("the structural loan must not be released through the elastic path")
	}

	// app to app
	if err := s.BorrowElastic(ctx, appAPI, appWeb, "cpu", 2); err != nil {
		t.Fatalf("BorrowElastic: %v", err)
	}
	if err := s.Return(ctx, appAPI, appWeb, "cpu", 2); err == nil {
		t.Error("Return must not settle an elastic loan")
	}
	if err := s.ReturnElastic(ctx, appAPI, appWeb, "cpu", 2); err != nil {
		t.Fatalf("ReturnElastic: %v", err)
	}
}

// pool borrows are checked against the pools free budget under its lock
func TestPoolBorrowRespectsFreeBudget(t *testing.T) {
	q := newFakeQuotas()
	q.set(appWeb, domain.ResourceQuotas{"cpu": 1})
	q.free["org1/ns1"] = domain.ResourceQuotas{"cpu": 1}
	s := newTestBorrowService(q, newFakeLoans(), &fakeDispatcher{})
	ctx := context.Background()

	if err := s.BorrowFromPool(ctx, "org1/ns1", appWeb, domain.ResourceQuotas{"cpu": 2}); err == nil {
		t.Error("pool borrow above the free budget should fail")
	}
	if err := s.BorrowFromNamespace(ctx, "org1/ns1", appWeb, domain.ResourceQuotas{"cpu": 2}); err == nil {
		t.Error("namespace borrow above the free budget should fail")
	}
	if web, _ := q.GetQuotas(appWeb); web["cpu"] != 1 {
		t.Errorf("rejected borrows changed web cpu to %g, want 1", web["cpu"])
	}
	if err := s.BorrowFromPool(ctx, "org1/ns1", appWeb, domain.ResourceQuotas{"cpu": 1}); err != nil {
		t.Errorf("pool borrow within the free budget should succeed: %v", err)
	}
}

// handing loans to a successor
func TestReassignDropsMutualLoanAndKeepsKinds(t *testing.T) {
	const web2 = "org1/ns1/web2"
	loans := newFakeLoans()
	loans.record(web2, appWeb, "cpu", 1, false)      // web borrowed 1 from its successor
	loans.record("org1/ns1", appWeb, "cpu", 2, true) // web grew 2 into the namespace pool
	s := newTestBorrowService(newFakeQuotas(), loans, &fakeDispatcher{})

	if err := s.ReassignLoansToSuccessor(context.Background(), appWeb, web2); err != nil {
		t.Fatalf("ReassignLoansToSuccessor: %v", err)
	}
	if l, _ := loans.Involving(appWeb); len(l) != 0 {
		t.Errorf("web should have no loans left, got %+v", l)
	}
	if out, _ := loans.Outstanding(web2, web2, "cpu", false); out != 0 {
		t.Errorf("mutual loan became a self-loan: %g", out)
	}
	if out, _ := loans.Outstanding("org1/ns1", web2, "cpu", true); out != 2 {
		t.Errorf("elastic pool loan should move to web2 as elastic, got %g", out)
	}
}

// ---- in-memory fakes -----

type fakeQuotas struct {
	m    map[string]domain.ResourceQuotas
	free map[string]domain.ResourceQuotas
}

func newFakeQuotas() *fakeQuotas {
	return &fakeQuotas{m: map[string]domain.ResourceQuotas{}, free: map[string]domain.ResourceQuotas{}}
}

func (f *fakeQuotas) Available(entityId string) (domain.ResourceQuotas, error) {
	return cloneQuotas(f.free[entityId]), nil
}

func (f *fakeQuotas) set(id string, q domain.ResourceQuotas) { f.m[id] = cloneQuotas(q) }

func (f *fakeQuotas) ListApps() ([]string, error) {
	ids := make([]string, 0, len(f.m))
	for id := range f.m {
		ids = append(ids, id)
	}
	return ids, nil
}

func (f *fakeQuotas) GetQuotas(id string) (domain.ResourceQuotas, error) {
	q, ok := f.m[id]
	if !ok {
		return nil, fmt.Errorf("no quotas for %s", id)
	}
	return cloneQuotas(q), nil
}

func (f *fakeQuotas) SetQuotas(id string, q domain.ResourceQuotas) error {
	f.m[id] = cloneQuotas(q)
	return nil
}

func cloneQuotas(q domain.ResourceQuotas) domain.ResourceQuotas {
	c := make(domain.ResourceQuotas, len(q))
	for k, v := range q {
		c[k] = v
	}
	return c
}

// keeps structural and elastic loans between the same pair apart like the Neo4j store
type fakeLoans struct{ m map[string]float64 }

func newFakeLoans() *fakeLoans { return &fakeLoans{m: map[string]float64{}} }

func loanKey(from, to, res string, elastic bool) string {
	kind := "s"
	if elastic {
		kind = "e"
	}
	return from + "|" + to + "|" + res + "|" + kind
}

func (f *fakeLoans) record(from, to, res string, amt float64, elastic bool) error {
	f.m[loanKey(from, to, res, elastic)] += amt
	return nil
}

func (f *fakeLoans) reduce(from, to, res string, amt float64, elastic bool) error {
	k := loanKey(from, to, res, elastic)
	f.m[k] -= amt
	if f.m[k] <= 0 {
		delete(f.m, k)
	}
	return nil
}

func (f *fakeLoans) Outstanding(from, to, res string, elastic bool) (float64, error) {
	return f.m[loanKey(from, to, res, elastic)], nil
}

func (f *fakeLoans) BorrowedBy(app, res string) (float64, error) {
	var sum float64
	for k, v := range f.m {
		p := strings.Split(k, "|")
		if p[1] == app && p[2] == res {
			sum += v
		}
	}
	return sum, nil
}

func (f *fakeLoans) List() ([]domain.Loan, error) {
	out := make([]domain.Loan, 0, len(f.m))
	for k, v := range f.m {
		p := strings.Split(k, "|")
		out = append(out, domain.Loan{From: p[0], To: p[1], Resource: p[2], Amount: v, Elastic: p[3] == "e"})
	}
	return out, nil
}

func (f *fakeLoans) Involving(id string) ([]domain.Loan, error) {
	all, _ := f.List()
	var out []domain.Loan
	for _, ln := range all {
		if ln.From == id || ln.To == id {
			out = append(out, ln)
		}
	}
	return out, nil
}

func (f *fakeLoans) ReassignAll(oldApp, newApp string) error {
	keys := make([]string, 0, len(f.m))
	for k := range f.m {
		keys = append(keys, k)
	}
	for _, k := range keys {
		p := strings.Split(k, "|")
		from, to := p[0], p[1]
		if from != oldApp && to != oldApp {
			continue
		}
		amt := f.m[k]
		delete(f.m, k)
		if from == oldApp {
			from = newApp
		}
		if to == oldApp {
			to = newApp
		}
		if from == to {
			continue // loan between oldApp and newApp: dropped
		}
		f.m[loanKey(from, to, p[2], p[3] == "e")] += amt
	}
	return nil
}

type dispatchCall struct {
	org, ns, app string
	quotas       domain.ResourceQuotas
}

type fakeDispatcher struct{ calls []dispatchCall }

func (f *fakeDispatcher) DispatchQuotas(ctx context.Context, org, ns, app string, q domain.ResourceQuotas) error {
	f.calls = append(f.calls, dispatchCall{org, ns, app, cloneQuotas(q)})
	return nil
}

// applies borrows/returns against the in-memory fake quota + loan stores
type fakeTransfer struct {
	quotas *fakeQuotas
	loans  *fakeLoans
}

func (f *fakeTransfer) setVal(id, resource string, val float64) {
	q, err := f.quotas.GetQuotas(id)
	if err != nil {
		q = domain.ResourceQuotas{}
	}
	q[resource] = val
	_ = f.quotas.SetQuotas(id, q)
}

func (f *fakeTransfer) ApplyBorrow(from, to, resource string, fromVal, toVal, amount float64, elastic bool) error {
	f.setVal(from, resource, fromVal)
	f.setVal(to, resource, toVal)
	return f.loans.record(from, to, resource, amount, elastic)
}

func (f *fakeTransfer) ApplyReturn(from, to, resource string, fromVal, toVal, amount float64, elastic bool) error {
	f.setVal(from, resource, fromVal)
	f.setVal(to, resource, toVal)
	return f.loans.reduce(from, to, resource, amount, elastic)
}

func (f *fakeTransfer) ApplyNamespaceBorrow(fromNamespace, to, resource string, toVal, amount float64) error {
	f.setVal(to, resource, toVal)
	return f.loans.record(fromNamespace, to, resource, amount, false)
}

func (f *fakeTransfer) ApplyNamespaceReturn(fromNamespace, to, resource string, toVal, amount float64, elastic bool) error {
	f.setVal(to, resource, toVal)
	return f.loans.reduce(fromNamespace, to, resource, amount, elastic)
}

func (f *fakeTransfer) ApplyPoolBorrow(fromEntity, to, resource string, toVal, amount float64) error {
	f.setVal(to, resource, toVal)
	return f.loans.record(fromEntity, to, resource, amount, true)
}

func (f *fakeTransfer) ApplyLoanRepoint(oldLender, newLender, borrower, resource string, amount float64, oldElastic bool) error {
	if err := f.loans.reduce(oldLender, borrower, resource, amount, oldElastic); err != nil {
		return err
	}
	return f.loans.record(newLender, borrower, resource, amount, true)
}


func newTestBorrowService(q *fakeQuotas, loans *fakeLoans, disp *fakeDispatcher) *BorrowService {
	return NewBorrowService(q, loans, &fakeTransfer{quotas: q, loans: loans}, disp)
}
