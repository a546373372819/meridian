package services

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/c12s/meridian/internal/domain"
)

type fakeMetrics struct {
	util map[string]map[string]float64 // resource -> appId -> utilization
}

func (f fakeMetrics) Utilizations(_ context.Context, resource string) (map[string]float64, error) {
	return f.util[resource], nil
}

// runs the controller over the real BorrowService
func newTestController(quotas, util, free map[string]float64, loans []domain.Loan) (*StressController, *fakeQuotas, *fakeLoans) {
	q := newFakeQuotas()
	for app, cpu := range quotas {
		q.set(app, domain.ResourceQuotas{"cpu": cpu})
	}
	for pool, cpu := range free {
		q.free[pool] = domain.ResourceQuotas{"cpu": cpu}
	}
	fl := newFakeLoans()
	for _, ln := range loans {
		fl.record(ln.From, ln.To, ln.Resource, ln.Amount, ln.Elastic)
	}
	borrow := newTestBorrowService(q, fl, &fakeDispatcher{})
	c := NewStressController(fakeMetrics{util: map[string]map[string]float64{"cpu": util}}, q, borrow, fl)
	c.Target, c.High, c.Low = 0.5, 0.85, 0.30
	return c, q, fl
}

// returns every apps cpu quota.
func cpuOf(q *fakeQuotas) map[string]float64 {
	out := map[string]float64{}
	for app, quotas := range q.m {
		out[app] = quotas["cpu"]
	}
	return out
}

// renders the outstanding cpu loans as sorted "from-to amount kind"
func loanList(fl *fakeLoans) []string {
	all, _ := fl.List()
	var out []string
	for _, ln := range all {
		kind := "s"
		if ln.Elastic {
			kind = "e"
		}
		out = append(out, fmt.Sprintf("%s->%s %g %s", ln.From, ln.To, ln.Amount, kind))
	}
	sort.Strings(out)
	return out
}

// where a stressed app gets its extra quota from - nearby first (own namespace
// pool, then idle apps in its namespace, then the org pool, then sibling
// namespace pools, then idle apps elsewhere in the org), donors with the most
// spare first, never across organizations.
func TestStressControllerBorrowOrder(t *testing.T) {
	cases := []struct {
		name      string
		quotas    map[string]float64
		util      map[string]float64
		free      map[string]float64
		wantCPU   map[string]float64
		wantLoans []string
	}{
		{
			name:      "idle sibling lends",
			quotas:    map[string]float64{"org1/ns1/hot": 2, "org1/ns1/idle": 4},
			util:      map[string]float64{"org1/ns1/hot": 1.0, "org1/ns1/idle": 0.25},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4, "org1/ns1/idle": 2}, // idle spares 4 - 1/0.5 = 2
			wantLoans: []string{"org1/ns1/idle->org1/ns1/hot 2 e"},
		},
		{
			name:      "own namespace pool before any app",
			quotas:    map[string]float64{"org1/ns1/hot": 2, "org1/ns1/idle": 4},
			util:      map[string]float64{"org1/ns1/hot": 1.0, "org1/ns1/idle": 0.25},
			free:      map[string]float64{"org1/ns1": 2},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4, "org1/ns1/idle": 4},
			wantLoans: []string{"org1/ns1->org1/ns1/hot 2 e"},
		},
		{
			name:      "pool first, then a sibling for the rest",
			quotas:    map[string]float64{"org1/ns1/hot": 2, "org1/ns1/idle": 4},
			util:      map[string]float64{"org1/ns1/hot": 1.0, "org1/ns1/idle": 0.25},
			free:      map[string]float64{"org1/ns1": 1},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4, "org1/ns1/idle": 3},
			wantLoans: []string{"org1/ns1->org1/ns1/hot 1 e", "org1/ns1/idle->org1/ns1/hot 1 e"},
		},
		{
			name:      "donor with most spare first",
			quotas:    map[string]float64{"org1/ns1/hot": 2, "org1/ns1/big": 10, "org1/ns1/small": 4},
			util:      map[string]float64{"org1/ns1/hot": 1.0, "org1/ns1/big": 0.1, "org1/ns1/small": 0.25},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4, "org1/ns1/big": 8, "org1/ns1/small": 4},
			wantLoans: []string{"org1/ns1/big->org1/ns1/hot 2 e"},
		},
		{
			name:      "same-namespace donor before a bigger one elsewhere",
			quotas:    map[string]float64{"org1/ns1/hot": 2, "org1/ns1/local": 4, "org1/ns2/remote": 10},
			util:      map[string]float64{"org1/ns1/hot": 1.0, "org1/ns1/local": 0.25, "org1/ns2/remote": 0.1},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4, "org1/ns1/local": 2, "org1/ns2/remote": 10},
			wantLoans: []string{"org1/ns1/local->org1/ns1/hot 2 e"},
		},
		{
			name:      "other namespace when no local donor",
			quotas:    map[string]float64{"org1/ns1/hot": 2, "org1/ns2/remote": 10},
			util:      map[string]float64{"org1/ns1/hot": 1.0, "org1/ns2/remote": 0.1},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4, "org1/ns2/remote": 8},
			wantLoans: []string{"org1/ns2/remote->org1/ns1/hot 2 e"},
		},
		{
			name:      "org pool before apps",
			quotas:    map[string]float64{"org1/ns1/hot": 2},
			util:      map[string]float64{"org1/ns1/hot": 1.0},
			free:      map[string]float64{"org1": 5},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4},
			wantLoans: []string{"org1->org1/ns1/hot 2 e"},
		},
		{
			name:      "sibling namespace pool",
			quotas:    map[string]float64{"org1/ns1/hot": 2, "org1/ns2/calm": 4},
			util:      map[string]float64{"org1/ns1/hot": 1.0, "org1/ns2/calm": 0.5}, // calm: neither donor nor stressed
			free:      map[string]float64{"org1/ns2": 3},
			wantCPU:   map[string]float64{"org1/ns1/hot": 4, "org1/ns2/calm": 4},
			wantLoans: []string{"org1/ns2->org1/ns1/hot 2 e"},
		},
		{
			name:    "nothing stressed",
			quotas:  map[string]float64{"org1/ns1/a": 2, "org1/ns1/b": 2},
			util:    map[string]float64{"org1/ns1/a": 0.5, "org1/ns1/b": 0.5},
			wantCPU: map[string]float64{"org1/ns1/a": 2, "org1/ns1/b": 2},
		},
		{
			name:    "never across organizations",
			quotas:  map[string]float64{"org1/ns1/hot": 2, "org2/ns1/idle": 4},
			util:    map[string]float64{"org1/ns1/hot": 1.0, "org2/ns1/idle": 0.1},
			wantCPU: map[string]float64{"org1/ns1/hot": 2, "org2/ns1/idle": 4},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctrl, q, fl := newTestController(c.quotas, c.util, c.free, nil)
			if err := ctrl.Rebalance(context.Background(), "cpu"); err != nil {
				t.Fatalf("Rebalance: %v", err)
			}
			if got := cpuOf(q); !reflect.DeepEqual(got, c.wantCPU) {
				t.Errorf("cpu = %v, want %v", got, c.wantCPU)
			}
			if got := loanList(fl); !reflect.DeepEqual(got, c.wantLoans) {
				t.Errorf("loans = %v, want %v", got, c.wantLoans)
			}
		})
	}
}

// whast the controller takes back - only ELASTIC loans, only once the borrower is no longer busy, back to where they came from
func TestStressControllerReturns(t *testing.T) {
	poolLoan := domain.Loan{From: "org1/ns1", To: "org1/ns1/web", Resource: "cpu", Amount: 2, Elastic: true}
	appLoan := domain.Loan{From: "org1/ns1/api", To: "org1/ns1/web", Resource: "cpu", Amount: 2, Elastic: true}
	cases := []struct {
		name      string
		webUtil   float64
		loans     []domain.Loan
		wantCPU   map[string]float64
		wantLoans []string
	}{
		{"idle: pool loan back to the pool", 0.25, []domain.Loan{poolLoan},
			map[string]float64{"org1/ns1/web": 2, "org1/ns1/api": 2}, nil},
		{"idle: app loan back to the lender", 0.25, []domain.Loan{appLoan},
			map[string]float64{"org1/ns1/web": 2, "org1/ns1/api": 4}, nil},
		{"busy: keep the loan", 0.90, []domain.Loan{appLoan},
			map[string]float64{"org1/ns1/web": 4, "org1/ns1/api": 2},
			[]string{"org1/ns1/api->org1/ns1/web 2 e"}},
		{"structural loans are never taken back", 0.10, []domain.Loan{
			{From: "org1/ns1/api", To: "org1/ns1/web", Resource: "cpu", Amount: 2},
			{From: "org1/ns1", To: "org1/ns1/web", Resource: "cpu", Amount: 1},
		}, map[string]float64{"org1/ns1/web": 4, "org1/ns1/api": 2},
			[]string{"org1/ns1->org1/ns1/web 1 s", "org1/ns1/api->org1/ns1/web 2 s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctrl, q, fl := newTestController(
				map[string]float64{"org1/ns1/web": 4, "org1/ns1/api": 2},
				map[string]float64{"org1/ns1/web": c.webUtil, "org1/ns1/api": 0.5},
				nil, c.loans)
			if err := ctrl.Rebalance(context.Background(), "cpu"); err != nil {
				t.Fatalf("Rebalance: %v", err)
			}
			if got := cpuOf(q); !reflect.DeepEqual(got, c.wantCPU) {
				t.Errorf("cpu = %v, want %v", got, c.wantCPU)
			}
			if got := loanList(fl); !reflect.DeepEqual(got, c.wantLoans) {
				t.Errorf("loans = %v, want %v", got, c.wantLoans)
			}
		})
	}
}

// with several loans, the total returned must not exceed what the borrower can spare
func TestStressControllerReturnsAtMostSlackAcrossLoans(t *testing.T) {
	ctrl, q, _ := newTestController(
		map[string]float64{"org1/ns1/web": 10}, // usage 2
		map[string]float64{"org1/ns1/web": 0.2},
		nil,
		[]domain.Loan{
			{From: "org1/ns1", To: "org1/ns1/web", Resource: "cpu", Amount: 5, Elastic: true},
			{From: "org1/ns2", To: "org1/ns1/web", Resource: "cpu", Amount: 4, Elastic: true},
		})
	ctrl.Target = 0.7
	if err := ctrl.Rebalance(context.Background(), "cpu"); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	if got, want := q.m["org1/ns1/web"]["cpu"], 2/0.7; math.Abs(got-want) > 1e-9 {
		t.Errorf("web cpu = %g, want %g (no less than its usage needs)", got, want)
	}
}

// donor lending to two stressed apps in one pass must not go below Target
func TestStressControllerDoesNotOverLendDonorInOnePass(t *testing.T) {
	ctrl, q, _ := newTestController(
		map[string]float64{"org1/ns1/idle": 10, "org1/ns1/hot1": 4, "org1/ns1/hot2": 4},
		map[string]float64{"org1/ns1/idle": 0.2, "org1/ns1/hot1": 1.0, "org1/ns1/hot2": 1.0},
		nil, nil)
	if err := ctrl.Rebalance(context.Background(), "cpu"); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}

	if got := q.m["org1/ns1/idle"]["cpu"]; math.Abs(got-4) > 1e-9 {
		t.Errorf("idle cpu = %g, want 4 (lent its full spare 6 and no more)", got)
	}
}
