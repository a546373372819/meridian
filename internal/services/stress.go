package services

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/c12s/meridian/internal/domain"
)

// reports live app use of a resource
type MetricsProvider interface {
	Utilizations(ctx context.Context, resource string) (map[string]float64, error)
}

type StressController struct {
	metrics MetricsProvider
	quotas  QuotaAccessor
	borrow  *BorrowService
	loans   domain.LoanStore

	Target float64 
	High   float64 
	Low    float64 
}

func NewStressController(metrics MetricsProvider, quotas QuotaAccessor, borrow *BorrowService, loans domain.LoanStore) *StressController {
	return &StressController{
		metrics: metrics,
		quotas:  quotas,
		borrow:  borrow,
		loans:   loans,
		Target:  0.7,
		High:    0.85,
		Low:     0.30,
	}
}

// rebalances each resource every interval until ctx is cancelled
func (c *StressController) Run(ctx context.Context, resources []string, interval time.Duration) {
	runEvery(ctx, "stress", interval, func(ctx context.Context) {
		for _, resource := range resources {
			if err := c.Rebalance(ctx, resource); err != nil {
				log.Printf("stress: periodic rebalance of %s failed: %v", resource, err)
			}
		}
	})
}


func (c *StressController) Rebalance(ctx context.Context, resource string) error {
	util, err := c.metrics.Utilizations(ctx, resource)
	if err != nil {
		return err
	}

	quota := make(map[string]float64) 
	usage := make(map[string]float64)
	var stressed, donors []string
	for app, u := range util {
		q, err := c.quotas.GetQuotas(app)
		if err != nil {
			continue
		}
		quota[app] = q[resource]
		usage[app] = u * q[resource]
		switch {
		case u >= c.High:
			stressed = append(stressed, app)
		case u <= c.Low:
			donors = append(donors, app)
		}
	}

	lendable := func(app string) float64 {
		return quota[app] - usage[app]/c.Target
	}
	// relieve the most stressed app first
	sort.SliceStable(stressed, func(i, j int) bool {
		return util[stressed[i]] > util[stressed[j]]
	})
	// from the donor with the most spare capacity first
	sort.SliceStable(donors, func(i, j int) bool {
		return lendable(donors[i]) > lendable(donors[j])
	})


	poolFree := make(map[string]float64)
	freeFor := func(entity string) float64 {
		if v, ok := poolFree[entity]; ok {
			return v
		}
		v := 0.0
		if avail, err := c.quotas.Available(entity); err != nil {
			log.Printf("stress: read free budget of %s: %v", entity, err)
		} else {
			v = avail[resource]
		}
		poolFree[entity] = v
		return v
	}


	nsSeen := make(map[string]struct{})
	for app := range quota {
		nsSeen[domain.NamespaceOf(app)] = struct{}{}
	}
	allNamespaces := make([]string, 0, len(nsSeen))
	for ns := range nsSeen {
		allNamespaces = append(allNamespaces, ns)
	}
	sort.Strings(allNamespaces)

	for _, s := range stressed {
		needed := usage[s]/c.Target - quota[s]
		sNs := domain.NamespaceOf(s)


		takeFromPool := func(entity string) {
			if needed <= 0 {
				return
			}
			free := freeFor(entity)
			if free <= 0 {
				return
			}
			grow := needed
			if free < grow {
				grow = free
			}
			if grow <= 0 {
				return
			}
			if err := c.borrow.BorrowFromPool(ctx, entity, s, domain.ResourceQuotas{resource: grow}); err != nil {
				log.Printf("stress: grow %s from free pool %s failed: %v", s, entity, err)
				return
			}
			quota[s] += grow
			poolFree[entity] = free - grow
			needed -= grow
		}


		borrowFrom := func(d string) {
			if needed <= 0 {
				return
			}
			spare := lendable(d)
			if spare <= 0 {
				return
			}
			amount := needed
			if spare < amount {
				amount = spare
			}
			if amount <= 0 {
				return
			}
			if err := c.borrow.BorrowElastic(ctx, d, s, resource, amount); err != nil {
				log.Printf("stress: borrow %s -> %s failed: %v", d, s, err)
				return
			}
			quota[d] -= amount
			quota[s] += amount
			needed -= amount
		}

		// locality first waterfall 
		
		//   1. own namespace's free pool
		takeFromPool(sNs)
		//   2. idle sibling apps in the same namespace (most spare first)
		for _, d := range donors {
			if needed <= 0 {
				break
			}
			if domain.NamespaceOf(d) == sNs {
				borrowFrom(d)
			}
		}
		//   3. the orgs free pool 
		takeFromPool(domain.OrgOf(s))
		//   4. sibling namespaces free pools (same org)
		for _, ns := range allNamespaces {
			if needed <= 0 {
				break
			}
			if ns != sNs && domain.OrgOf(ns) == domain.OrgOf(s) {
				takeFromPool(ns)
			}
		}
		//   5. idle apps in other namespaces of the same org
		for _, d := range donors {
			if needed <= 0 {
				break
			}
			if domain.OrgOf(d) == domain.OrgOf(s) && domain.NamespaceOf(d) != sNs {
				borrowFrom(d)
			}
		}
	}

	// reclaim loans whose borrower has gone idle 
	loans, err := c.loans.List()
	if err != nil {
		log.Printf("stress: list loans: %v", err)
		return nil
	}
	for _, ln := range loans {
		if ln.Resource != resource || !ln.Elastic {
			continue
		}
		bq, okBq := quota[ln.To]
		bUsage, okBu := usage[ln.To]
		if !okBq || !okBu {
			continue 
		}
		
		if util[ln.To] >= c.High {
			continue 
		}
		
		slack := bq - bUsage/c.Target
		if slack <= 0 {
			continue
		}
		amount := ln.Amount
		if slack < amount {
			amount = slack
		}
		if amount <= 0 {
			continue
		}


		if !domain.IsAppId(ln.From) {
			if err := c.borrow.ReturnToNamespace(ctx, ln.From, ln.To, resource, amount, true); err != nil {
				log.Printf("stress: return-to-pool %s -> %s failed: %v", ln.To, ln.From, err)
				continue
			}
			quota[ln.To] -= amount
			continue
		}

		if err := c.borrow.ReturnElastic(ctx, ln.From, ln.To, resource, amount); err != nil {
			log.Printf("stress: return %s -> %s failed: %v", ln.To, ln.From, err)
			continue
		}
		quota[ln.To] -= amount
		if _, tracked := quota[ln.From]; tracked {
			quota[ln.From] += amount
		}
	}
	return nil
}
