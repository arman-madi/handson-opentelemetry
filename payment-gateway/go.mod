module github.com/arman-madi/handson-opentelemetry/payment-gateway

go 1.16

require (
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.25.0
	go.opentelemetry.io/otel v1.0.1
	go.opentelemetry.io/otel/exporters/zipkin v1.0.1
	go.opentelemetry.io/otel/oteltest v0.20.0 // indirect
	go.opentelemetry.io/otel/sdk v1.0.1
	go.opentelemetry.io/otel/trace v1.0.1
)
