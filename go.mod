module github.com/c12s/meridian

go 1.26.0

require (
	github.com/c12s/gravity v1.0.0
	github.com/c12s/magnetar v1.0.0
	github.com/c12s/metrics v0.0.0
	github.com/c12s/oort v1.0.0
	github.com/c12s/pulsar v1.0.0
	github.com/c12s/tug v0.0.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/nats-io/nats.go v1.35.0
	github.com/neo4j/neo4j-go-driver/v4 v4.4.7
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.12
)

// tug is used only by cmd/borrow-demo (a demo harness), not by the meridian
// service itself — the service reaches nodes via gravity/NATS, not Docker.
replace github.com/c12s/tug => ../tug

replace github.com/c12s/metrics => ../starometry

require (
	github.com/Azure/go-ansiterm v0.0.0-20260917205352-e937bb47801a // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/docker v27.3.1+incompatible // indirect
	github.com/docker/go-connections v0.5.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/nats-io/nkeys v0.4.7 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.46.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260720211330-0afa2a65878a // indirect
)

replace github.com/c12s/pulsar => ../pulsar

replace github.com/c12s/oort => ../oort

replace github.com/c12s/magnetar => ../magnetar

replace github.com/c12s/gravity => ../gravity
