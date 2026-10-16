
package metrics

import (
	"context"
	"fmt"

	"github.com/c12s/meridian/internal/services"
)

// one service usage metric
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}


type Source interface {
	Latest(ctx context.Context) ([]Sample, error)
}

// maps a meridian resource to the starometrys convention
var starometryMetricNames = map[string]string{
	"cpu":  "custom_service_cpu_usage",
	"mem":  "custom_service_ram_usage_mb",
	"disk": "custom_service_disk_usage_mb",
}

// reads service usage from a source, maps each service to a meridian app, and divides by the apps quota to yield utilization
type UtilizationProvider struct {
	source Source
	quotas services.QuotaAccessor
	appID  func(serviceName string) string 
}

func NewUtilizationProvider(source Source, quotas services.QuotaAccessor, appID func(string) string) *UtilizationProvider {
	return &UtilizationProvider{source: source, quotas: quotas, appID: appID}
}

func (p *UtilizationProvider) Utilizations(ctx context.Context, resource string) (map[string]float64, error) {
	metricName, ok := starometryMetricNames[resource]
	if !ok {
		return nil, fmt.Errorf("metrics: no usage metric maps to resource %q", resource)
	}
	samples, err := p.source.Latest(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64)
	for _, s := range samples {
		if s.Name != metricName {
			continue
		}
		service := s.Labels["service_name"]
		if service == "" {
			continue
		}
		appID := p.appID(service)
		q, err := p.quotas.GetQuotas(appID)
		if err != nil {
			continue
		}
		quota := q[resource]
		if quota <= 0 {
			continue 
		}
		out[appID] = s.Value / quota
	}
	return out, nil
}
