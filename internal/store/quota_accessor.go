package store

import "github.com/c12s/meridian/internal/domain"

// adapts the ResourceQuotaStore
type QuotaStoreAccessor struct {
	store domain.ResourceQuotaStore
}

func NewQuotaStoreAccessor(store domain.ResourceQuotaStore) *QuotaStoreAccessor {
	return &QuotaStoreAccessor{store: store}
}

func (a *QuotaStoreAccessor) GetQuotas(appId string) (domain.ResourceQuotas, error) {
	return a.store.GetResourceQuotas(appId)
}

func (a *QuotaStoreAccessor) SetQuotas(appId string, quotas domain.ResourceQuotas) error {
	return a.store.SetResourceQuotas(appId, quotas, nil)
}

// entitys free budget, borrowing aware
func (a *QuotaStoreAccessor) Available(entityId string) (domain.ResourceQuotas, error) {
	return a.store.GetEffectiveAvailableResources(nil, entityId)
}

// every app id
func (a *QuotaStoreAccessor) ListApps() ([]string, error) {
	return a.store.ListAppIds()
}
