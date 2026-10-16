package metrics

import (
	"context"
	"testing"

	"github.com/c12s/meridian/internal/domain"
)

type fakeSource struct{ samples []Sample }

func (f fakeSource) Latest(context.Context) ([]Sample, error) { return f.samples, nil }

type fakeQuotas struct {
	m map[string]domain.ResourceQuotas
}

func (f fakeQuotas) GetQuotas(id string) (domain.ResourceQuotas, error) { return f.m[id], nil }
func (f fakeQuotas) SetQuotas(id string, q domain.ResourceQuotas) error { f.m[id] = q; return nil }
func (f fakeQuotas) ListApps() ([]string, error)                        { return nil, nil }
func (f fakeQuotas) Available(string) (domain.ResourceQuotas, error)    { return nil, nil }

func TestUtilizationProvider(t *testing.T) {
	source := fakeSource{samples: []Sample{
		{Name: "custom_service_cpu_usage", Labels: map[string]string{"service_name": "api"}, Value: 1.5},
		{Name: "custom_service_ram_usage_mb", Labels: map[string]string{"service_name": "api"}, Value: 256},
		{Name: "custom_node_cpu_usage_percentage", Labels: map[string]string{}, Value: 40}, // ignored (node-level)
	}}
	quotas := fakeQuotas{m: map[string]domain.ResourceQuotas{
		"org1/ns1/api": {"cpu": 2, "mem": 512},
	}}
	p := NewUtilizationProvider(source, quotas, func(svc string) string { return "org1/ns1/" + svc })

	cpu, err := p.Utilizations(context.Background(), "cpu")
	if err != nil {
		t.Fatalf("Utilizations(cpu): %v", err)
	}
	if cpu["org1/ns1/api"] != 0.75 { // 1.5 / 2
		t.Errorf("cpu utilization = %g, want 0.75", cpu["org1/ns1/api"])
	}

	mem, err := p.Utilizations(context.Background(), "mem")
	if err != nil {
		t.Fatalf("Utilizations(mem): %v", err)
	}
	if mem["org1/ns1/api"] != 0.5 { // 256 / 512
		t.Errorf("mem utilization = %g, want 0.5", mem["org1/ns1/api"])
	}
}
