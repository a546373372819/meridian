package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/c12s/meridian/internal/domain"
)

// reads and writes an apps resource quotas, and reads how much of an entitys budget is still unassigned
type QuotaAccessor interface {
	GetQuotas(appId string) (domain.ResourceQuotas, error)
	SetQuotas(appId string, quotas domain.ResourceQuotas) error
	
	// returns the unassigned budget of an entity.
	Available(entityId string) (domain.ResourceQuotas, error)
	
	// returns the id of every app (org/ns/app)
	ListApps() ([]string, error)
}

// pushes apps new quotas to the node running it
type Dispatcher interface {
	DispatchQuotas(ctx context.Context, orgId, namespaceName, appName string, quotas domain.ResourceQuotas) error
}

// moves resource budget between two apps in the same namespace and records the loan
type BorrowService struct {
	quotas     QuotaAccessor
	loans      domain.LoanStore
	transfer   TransferStore
	dispatcher Dispatcher
	locks      keyedMutex
}

func NewBorrowService(quotas QuotaAccessor, loans domain.LoanStore, transfer TransferStore, dispatcher Dispatcher) *BorrowService {
	return &BorrowService{quotas: quotas, loans: loans, transfer: transfer, dispatcher: dispatcher}
}

// moves resource from lender app to borrower app as a loan
func (s *BorrowService) Borrow(ctx context.Context, from, to, resource string, amount float64) error {
	return s.borrow(ctx, from, to, resource, amount, false)
}

// borrow for the stress controller, the loan is marked elastic so the controller reclaims it when the borrower slows down
func (s *BorrowService) BorrowElastic(ctx context.Context, from, to, resource string, amount float64) error {
	return s.borrow(ctx, from, to, resource, amount, true)
}

func (s *BorrowService) borrow(ctx context.Context, from, to, resource string, amount float64, elastic bool) error {
  
	if err := validateTransfer(from, to, resource, amount); err != nil {
		return err
	}

	unlock := s.locks.lockTwo(from, to)
	defer unlock()

	fromQ, err := s.quotas.GetQuotas(from)  
	if err != nil {
		return fmt.Errorf("read lender quotas: %w", err)
	}
  
	toQ, err := s.quotas.GetQuotas(to)
	if err != nil {
		return fmt.Errorf("read borower quotas: %w", err)
	}

	borrowed, err := s.loans.BorrowedBy(from, resource)
	if err != nil {
		return fmt.Errorf("read lender loans: %w", err)
	}
  
	if own := fromQ[resource] - borrowed; own < amount {
		return domain.Precondition("lender %s can lend at most %g %s of its own (holds %g, of which %g is borrowed), cannot lend %g",
			from, own, resource, fromQ[resource], borrowed, amount)
	}

	newFrom := fromQ[resource] - amount
	newTo := toQ[resource] + amount

	if err := s.transfer.ApplyBorrow(from, to, resource, newFrom, newTo, amount, elastic); err != nil {
		return fmt.Errorf("apply borrow: %w", err)
	}

	fromQ[resource] = newFrom
	toQ[resource] = newTo
	s.dispatch(ctx, from, fromQ)
	s.dispatch(ctx, to, toQ)
	return nil
}

// checks that entitys free budget covers the requested amounts
func (s *BorrowService) poolHasRoom(entity string, want domain.ResourceQuotas) error {
	free, err := s.quotas.Available(entity)
	if err != nil {
		return fmt.Errorf("read free budget of %s: %w", entity, err)
	}
	for resource, amount := range want {
		if amount > 0 && free[resource] < amount {
			return domain.Precondition("%s has only %g %s free, cannot lend %g", entity, free[resource], resource, amount)
		}
	}
	return nil
}


func (s *BorrowService) ReturnToNamespace(ctx context.Context, fromNamespace, toApp, resource string, amount float64, elastic bool) error {
	if !isFinite(amount) {
		return domain.Invalid("amount must be a finite number, got %g", amount)
	}
	if amount <= 0 {
		return nil
	}
	if !slices.Contains(domain.SupportedResourceQuotas, resource) {
		return domain.Invalid("unsupported resource %q", resource)
	}
	unlock := s.locks.lockTwo(fromNamespace, toApp)
	defer unlock()

	outstanding, err := s.loans.Outstanding(fromNamespace, toApp, resource, elastic)
	if err != nil {
		return fmt.Errorf("read namespace loan: %w", err)
	}
	if outstanding < amount {
		return domain.Precondition("cannot return %g %s: only %g owed by %s to namespace %s", amount, resource, outstanding, toApp, fromNamespace)
	}

	toQ, err := s.quotas.GetQuotas(toApp)
	if err != nil {
		return fmt.Errorf("read borrower quotas: %w", err)
	}
	if toQ[resource] < amount {
		return domain.Precondition("borrower %s holds %g %s, cannot return %g", toApp, toQ[resource], resource, amount)
	}
	newTo := toQ[resource] - amount

	// atomic - the borrowers quota shrink and the loan reduction commit together
	if err := s.transfer.ApplyNamespaceReturn(fromNamespace, toApp, resource, newTo, amount, elastic); err != nil {
		return fmt.Errorf("apply namespace return: %w", err)
	}
	toQ[resource] = newTo
	s.dispatch(ctx, toApp, toQ)
	return nil
}


func (s *BorrowService) InheritResources(ctx context.Context, toApp string, inherited domain.ResourceQuotas) error {
	if len(inherited) == 0 {
		return nil
	}
	unlock := s.locks.lock(toApp)
	defer unlock()
	toQ, err := s.quotas.GetQuotas(toApp)
	if err != nil {
		return fmt.Errorf("read successor quotas: %w", err)
	}
	for resource, amount := range inherited {
		toQ[resource] += amount
	}
	if err := s.quotas.SetQuotas(toApp, toQ); err != nil {
		return fmt.Errorf("write successor quotas: %w", err)
	}
	s.dispatch(ctx, toApp, toQ)
	return nil
}


func (s *BorrowService) BorrowFromNamespace(ctx context.Context, fromNamespace, toApp string, inherited domain.ResourceQuotas) error {
	if len(inherited) == 0 {
		return nil
	}
	if err := validAmounts(inherited); err != nil {
		return err
	}
	if domain.OrgOf(fromNamespace) != domain.OrgOf(toApp) {
		return domain.Invalid("borrowing only allowed within the same organization (%s vs %s)", domain.OrgOf(fromNamespace), domain.OrgOf(toApp))
	}

	unlock := s.locks.lockTwo(fromNamespace, toApp)
	defer unlock()

	toQ, err := s.quotas.GetQuotas(toApp)
	if err != nil {
		return fmt.Errorf("read successor quotas: %w", err)
	}
	if err := s.poolHasRoom(fromNamespace, inherited); err != nil {
		return err
	}
	for resource, amount := range inherited {
		if amount <= 0 {
			continue
		}
		newTo := toQ[resource] + amount
		// atomic per resource - grow the borrower and record the loan together
		if err := s.transfer.ApplyNamespaceBorrow(fromNamespace, toApp, resource, newTo, amount); err != nil {
			return fmt.Errorf("apply namespace borrow: %w", err)
		}
		toQ[resource] = newTo
	}
	s.dispatch(ctx, toApp, toQ)
	return nil
}


func (s *BorrowService) BorrowFromPool(ctx context.Context, fromEntity, toApp string, inherited domain.ResourceQuotas) error {
	if len(inherited) == 0 {
		return nil
	}
	if err := validAmounts(inherited); err != nil {
		return err
	}
	if domain.OrgOf(fromEntity) != domain.OrgOf(toApp) {
		return domain.Invalid("borrowing only allowed within the same organization (%s vs %s)", domain.OrgOf(fromEntity), domain.OrgOf(toApp))
	}

	// recheck budget
	unlock := s.locks.lockTwo(fromEntity, toApp)
	defer unlock()

	toQ, err := s.quotas.GetQuotas(toApp)
	if err != nil {
		return fmt.Errorf("read borrower quotas: %w", err)
	}
	
	if err := s.poolHasRoom(fromEntity, inherited); err != nil {
		return err
	}
	
	for resource, amount := range inherited {
		if amount <= 0 {
			continue
		}
		newTo := toQ[resource] + amount
		if err := s.transfer.ApplyPoolBorrow(fromEntity, toApp, resource, newTo, amount); err != nil {
			return fmt.Errorf("apply pool borrow: %w", err)
		}
		toQ[resource] = newTo
	}
	
	s.dispatch(ctx, toApp, toQ)
	return nil
}


func (s *BorrowService) Return(ctx context.Context, from, to, resource string, amount float64) error {
	return s.giveBack(ctx, from, to, resource, amount, false)
}

func (s *BorrowService) ReturnElastic(ctx context.Context, from, to, resource string, amount float64) error {
	return s.giveBack(ctx, from, to, resource, amount, true)
}

func (s *BorrowService) giveBack(ctx context.Context, from, to, resource string, amount float64, elastic bool) error {
	if err := validateTransfer(from, to, resource, amount); err != nil {
		return err
	}

	unlock := s.locks.lockTwo(from, to)
	defer unlock()

	outstanding, err := s.loans.Outstanding(from, to, resource, elastic)
	if err != nil {
		return fmt.Errorf("read loan: %w", err)
	}
	if outstanding < amount {
		return domain.Precondition("cannot return %g %s: only %g outstanding from %s to %s", amount, resource, outstanding, from, to)
	}

	fromQ, err := s.quotas.GetQuotas(from)
	if err != nil {
		return fmt.Errorf("read lender quotas: %w", err)
	}
	toQ, err := s.quotas.GetQuotas(to)
	if err != nil {
		return fmt.Errorf("read borrower quotas: %w", err)
	}
	if toQ[resource] < amount {
		return domain.Precondition("borrower %s holds %g %s, cannot return %g", to, toQ[resource], resource, amount)
	}

	newTo := toQ[resource] - amount
	newFrom := fromQ[resource] + amount

	if err := s.transfer.ApplyReturn(from, to, resource, newFrom, newTo, amount, elastic); err != nil {
		return fmt.Errorf("apply return: %w", err)
	}

	fromQ[resource] = newFrom
	toQ[resource] = newTo
	s.dispatch(ctx, from, fromQ)
	s.dispatch(ctx, to, toQ)
	return nil
}


func (s *BorrowService) ReassignLoansToSuccessor(ctx context.Context, oldApp, newApp string) error {

	for attempt := 0; attempt < 5; attempt++ {
		parties, err := s.loanParties(oldApp)
		if err != nil {
			return err
		}
		unlock := s.locks.lockMany(append([]string{oldApp, newApp}, parties...)...)
		now, err := s.loanParties(oldApp)
		if err == nil && slices.Equal(parties, now) {
			err = s.loans.ReassignAll(oldApp, newApp)
			unlock()
			return err
		}
		unlock()
		if err != nil {
			return err
		}
	}
	return domain.Precondition("loans of %s kept changing while being reassigned; try again", oldApp)
}

// returns sorted every entity that has a loan with app
func (s *BorrowService) loanParties(app string) ([]string, error) {
	loans, err := s.loans.Involving(app)
	if err != nil {
		return nil, err
	}
	var parties []string
	for _, ln := range loans {
		other := ln.From
		if other == app {
			other = ln.To
		}
		parties = append(parties, other)
	}
	slices.Sort(parties)
	return slices.Compact(parties), nil
}


func (s *BorrowService) SettleLoans(ctx context.Context, app string) error {
	loans, err := s.loans.Involving(app)
	if err != nil {
		return err
	}
	var errs []error
	for _, ln := range loans {
		switch {
		case ln.To == app && domain.IsAppId(ln.From):
			if err := s.giveBack(ctx, ln.From, app, ln.Resource, ln.Amount, ln.Elastic); err != nil {
				errs = append(errs, fmt.Errorf("return %s -> %s: %w", app, ln.From, err))
			}
		case ln.To == app:
			if err := s.ReturnToNamespace(ctx, ln.From, app, ln.Resource, ln.Amount, ln.Elastic); err != nil {
				errs = append(errs, fmt.Errorf("return-to-pool %s -> %s: %w", app, ln.From, err))
			}
		case ln.From == app:

			if err := s.repointLentToNamespace(ctx, app, ln.To, ln.Resource, ln.Amount, ln.Elastic); err != nil {
				errs = append(errs, fmt.Errorf("repoint %s <- %s: %w", app, ln.To, err))
			}
		}
	}
	return errors.Join(errs...)
}


func (s *BorrowService) repointLentToNamespace(ctx context.Context, lenderApp, borrower, resource string, amount float64, fromElastic bool) error {
	if amount <= 0 {
		return nil
	}
	namespace := domain.NamespaceOf(lenderApp)
	unlock := s.locks.lockMany(lenderApp, borrower, namespace)
	defer unlock()
	if err := s.transfer.ApplyLoanRepoint(lenderApp, namespace, borrower, resource, amount, fromElastic); err != nil {
		return fmt.Errorf("repoint lent loan: %w", err)
	}
	return nil
}

// ushes apps current quotas to its nodes
func (s *BorrowService) SyncApp(ctx context.Context, appId string) error {
	unlock := s.locks.lock(appId)
	defer unlock()
	q, err := s.quotas.GetQuotas(appId)
	if err != nil {
		return fmt.Errorf("read quotas of %s: %w", appId, err)
	}
	s.dispatch(ctx, appId, q)
	return nil
}

// bounds one whole dispatch
const dispatchTimeout = 60 * time.Second

func (s *BorrowService) dispatch(ctx context.Context, appId string, quotas domain.ResourceQuotas) {
	org, ns, app, ok := domain.SplitAppId(appId)
	if !ok {
		log.Printf("borrow: cannot parse app id %q for dispatch", appId)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()
	if err := s.dispatcher.DispatchQuotas(ctx, org, ns, app, quotas); err != nil {
		log.Printf("borrow: dispatch %s failed: %v", appId, err)
	}
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func validAmounts(q domain.ResourceQuotas) error {
	for resource, amount := range q {
		if !isFinite(amount) {
			return domain.Invalid("amount of %s must be a finite number, got %g", resource, amount)
		}
	}
	return nil
}

func validateTransfer(from, to, resource string, amount float64) error {
  
	if !isFinite(amount) || amount <= 0 {
		return domain.Invalid("amount must be a finite number > 0, got %g", amount)
	}
  
	if from == to {
		return domain.Invalid("cannot borrow from self")
	}
  
	if !slices.Contains(domain.SupportedResourceQuotas, resource) {
		return domain.Invalid("unsupported resource %q", resource)
	}

	if domain.OrgOf(from) != domain.OrgOf(to) {
		return domain.Invalid("borrowing only allowed within the same organization (%s vs %s)", domain.OrgOf(from), domain.OrgOf(to))
	}
	return nil
}

type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = make(map[string]*sync.Mutex)
	}
	mu, ok := k.m[key]
	if !ok {
		mu = &sync.Mutex{}
		k.m[key] = mu
	}
	k.mu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func (k *keyedMutex) lockTwo(a, b string) func() {
	return k.lockMany(a, b)
}


func (k *keyedMutex) lockMany(keys ...string) func() {
	sorted := slices.Clone(keys)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	unlocks := make([]func(), 0, len(sorted))
	for _, key := range sorted {
		unlocks = append(unlocks, k.lock(key))
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}
