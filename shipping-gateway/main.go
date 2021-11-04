package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

type Shipping struct {
	Address string   `json:"address"`
	Vendor  string   `json:"vendor"`
	Basket  []string `json:"basket"`
}

var logger = log.New(os.Stderr, "[shipping-gateway] ", log.Ldate|log.Ltime|log.Llongfile)

// Create one tracer per package
// NOTE: You only need a tracer if you are creating your own spans
var tracer trace.Tracer

// initTracer creates a new trace provider instance and registers it as global trace provider.
func initTracer() /*(*sdktrace.TracerProvider, error)*/ func() {

	// ** STDOUT Exporter
	stdoutExporter, err := stdouttrace.New( /*stdouttrace.WithPrettyPrint()*/ )
	if err != nil {
		log.Fatal("failed to initialize stdouttrace exporter: ", err)
	}

	// ** Jaeger Exporter
	jaegerUrl := "http://jaeger:14268/api/traces"
	jaegerExporter, err := jaeger.New(
		jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(jaegerUrl)),
	)
	if err != nil {
		log.Fatal("failed to initialize jaeger exporter: ", err)
	}

	// ** Zipkin Exporter
	zipkinUrl := "http://zipkin:9411/api/v2/spans"
	zipkinExporter, err := zipkin.New(
		zipkinUrl,
		// zipkin.WithLogger(logger),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ** Trace Provider
	// For demoing purposes, always sample. In a production application, you should
	// configure the sampler to a trace.ParentBased(trace.TraceIDRatioBased) set at the desired
	// ratio.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(zipkinExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithBatcher(jaegerExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithBatcher(stdoutExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("shipping-gateway"),
			attribute.String("environment", "demo"),
			attribute.Int64("ID", 3),
		)),
	)

	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Register our TracerProvider as the global so any imported
	// instrumentation in the future will default to using it.
	otel.SetTracerProvider(tp)

	// Name the tracer after the package, or the service if you are in main
	tracer = otel.Tracer("handson-opentelemetry/shipping-gateway")

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func main() {
	logger.Println("Hello, this is shipping-gateway service which is responsible to dispatch user shipping requests in order to demonestrate how OpenTelemetry works!")

	shutdown := initTracer()
	defer shutdown()

	shippingHandler := func(w http.ResponseWriter, req *http.Request) {

		ctx := req.Context()
		span := trace.SpanFromContext(ctx)
		traceId := span.SpanContext().TraceID().String()
		logger.Printf("Handle request with trace id: %+v\n", traceId)

		var shipping Shipping
		err := json.NewDecoder(req.Body).Decode(&shipping)
		if err != nil {
			span.AddEvent("Error decoding shipping json", trace.WithAttributes(attribute.Key("err").String(err.Error())))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		logger.Printf("New request received: %+v\n", shipping)

		send(ctx, shipping)

		_, _ = io.WriteString(w, fmt.Sprintf("{\"trace-id\": \"%v\"}\n", traceId))
	}

	otelHandler := otelhttp.NewHandler(http.HandlerFunc(shippingHandler), "handle-shipping")

	http.Handle("/", otelHandler)
	logger.Printf("Listening on port 80\n")
	http.ListenAndServe(":80", nil)
}

func send(ctx context.Context, shipping Shipping) {
	client := http.DefaultClient

	payload := fmt.Sprintf("{\"address\":\"%s\", \"basket\":[\"%s\"]}", shipping.Address, strings.Join(shipping.Basket, "\",\""))
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/", shipping.Vendor), bytes.NewBuffer([]byte(payload)))

	// _, req = otelhttptrace.W3C(ctx, req)
	otelhttptrace.Inject(ctx, req,
		// It seems otelhttptrace.W3C didn't consider global propagator, so you must explecitly inject
		otelhttptrace.WithPropagators(propagation.TraceContext{}),
	)

	logger.Printf("Sending request to %s ...\n", shipping.Vendor)
	res, err := client.Do(req)

	span := trace.SpanFromContext(ctx)
	if err != nil {
		span.AddEvent(fmt.Sprintf("Error sending %s request", shipping.Vendor), trace.WithAttributes(attribute.Key("err").String(err.Error())))
		return
	}

	if res.StatusCode == 200 {
		span.AddEvent("Successfully paid", trace.WithAttributes(attribute.Key("shipping-method").String(shipping.Vendor)))
	} else {
		span.AddEvent(fmt.Sprintf("Error shipping with %s", shipping.Vendor), trace.WithAttributes(attribute.Key("status").Int(res.StatusCode)))
	}
}

// Using otelHttp in the below didn't propagate the right parent-id, so I used the above implementation!.
// func send(ctx context.Context, shipping Shipping) {

// 	span := trace.SpanFromContext(ctx)

// 	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
// 	payload := fmt.Sprintf("{\"address\":\"%s\"}", shipping.Address)
// 	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://%s/", shipping.Vendor), bytes.NewBuffer([]byte(payload)))

// 	logger.Printf("Sending request to %s ...\n", shipping.Vendor)
// 	res, err := client.Do(req)
// 	if err != nil {
// 		span.AddEvent(fmt.Sprintf("Error sending %s request", shipping.Vendor), trace.WithAttributes(attribute.Key("err").String(err.Error())))
// 		return
// 	}

// 	if res.StatusCode == 200 {
// 		span.AddEvent("Successfully paid", trace.WithAttributes(attribute.Key("shipping-method").String(shipping.Vendor)))
// 	} else {
// 		span.AddEvent(fmt.Sprintf("Error shipping with %s", shipping.Vendor), trace.WithAttributes(attribute.Key("status").Int(res.StatusCode)))
// 	}
// }
