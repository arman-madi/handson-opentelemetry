module github.com/arman-madi/handson-opentelemetry/back-end

go 1.16

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.26.0
	go.opentelemetry.io/otel v1.1.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric v0.24.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v0.24.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.1.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.1.0
	go.opentelemetry.io/otel/metric v0.24.0
	go.opentelemetry.io/otel/sdk v1.1.0
	go.opentelemetry.io/otel/sdk/metric v0.24.0
	go.opentelemetry.io/otel/trace v1.1.0
	google.golang.org/grpc v1.41.0
)
