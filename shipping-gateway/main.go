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
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

type Shipping struct {
	Address string   `json:"address"`
	Vendor  string   `json:"vendor"`
	Basket  []string `json:"basket"`
}

var logger = log.New(os.Stderr, "[shipping-gateway] ", log.Ldate|log.Ltime|log.Llongfile)

// Initializes an OTLP exporter, and configures the corresponding trace and
// metric providers.
func initProvider() func() {
	ctx := context.Background()

	otelAgentAddr := "otel-collector:4317"

	traceClient := otlptracegrpc.NewClient(
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(otelAgentAddr),
		otlptracegrpc.WithDialOption(grpc.WithBlock()))
	
	traceExp, err := otlptrace.New(ctx, traceClient)
	handleErr(err, "Failed to create the collector trace exporter")
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceNameKey.String("shipping-gateway"),
		),
	)
	handleErr(err, "failed to create resource")
	bsp := sdktrace.NewBatchSpanProcessor(traceExp)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	// set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetTracerProvider(tracerProvider)

	return func() {
		cxt, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := traceExp.Shutdown(cxt); err != nil {
			otel.Handle(err)
		}
	}
}

func handleErr(err error, message string) {
	if err != nil {
		log.Fatalf("%s: %v", message, err)
	}
}

func main() {
	logger.Println("Hello, this is shipping-gateway service which is responsible to dispatch user shipping requests in order to demonestrate how OpenTelemetry works!")

	shutdown := initProvider()
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
