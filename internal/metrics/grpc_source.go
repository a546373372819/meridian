package metrics

import (
	"context"

	metricsapi "github.com/c12s/metrics/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// reads the latest metrics from one or more starometry instances over gRPC and flattens them into Samples
type GRPCSource struct {
	clients []metricsapi.MetricsClient
}


func NewGRPCSource(addresses ...string) (*GRPCSource, error) {
	s := &GRPCSource{}
	for _, addr := range addresses {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		s.clients = append(s.clients, metricsapi.NewMetricsClient(conn))
	}
	return s, nil
}

func (s *GRPCSource) Latest(ctx context.Context) ([]Sample, error) {
	var out []Sample
	for _, c := range s.clients {
		resp, err := c.GetLatestMetrics(ctx, &metricsapi.GetLatestMetricsReq{})
		if err != nil {
			continue // best-effort: skip an unreachable starometry instance
		}
		data := resp.GetData()
		if data == nil {
			continue
		}
		for _, m := range data.GetMetrics() {
			out = append(out, Sample{
				Name:   m.GetMetricName(),
				Labels: m.GetLabels(),
				Value:  m.GetValue(),
			})
		}
	}
	return out, nil
}
