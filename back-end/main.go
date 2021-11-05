package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/propagation"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

type Order struct {
	Name     string   `json:"name"`
	Address  string   `json:"address"`
	Payment  string   `json:"payment"`
	Shipping string   `json:"shipping"`
	Basket   []string `json:"basket"`
}

var logger = log.New(os.Stderr, "[back-end] ", log.Ldate|log.Ltime|log.Llongfile)

// Create one tracer per package
// NOTE: You only need a tracer if you are creating your own spans
var tracer trace.Tracer

// Initializes an OTLP exporter, and configures the corresponding trace and
// metric providers.
func initProvider() func() {
	ctx := context.Background()

	otelAgentAddr := "otel-collector:4317"
	metricClient := otlpmetricgrpc.NewClient(
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithEndpoint(otelAgentAddr))
	metricExp, err := otlpmetric.New(ctx, metricClient)
	handleErr(err, "Failed to create the collector metric exporter")
	pusher := controller.New(
		processor.NewFactory(
			simple.NewWithExactDistribution(),
			metricExp,
		),
		controller.WithExporter(metricExp),
		controller.WithCollectPeriod(2*time.Second),
	)
	global.SetMeterProvider(pusher)
	err = pusher.Start(ctx)
	handleErr(err, "Failed to start metric pusher")

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
			semconv.ServiceNameKey.String("backend"),
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
		// pushes any last exports to the receiver
		if err := pusher.Stop(cxt); err != nil {
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
	logger.Println("Hello, this is back-end service which is first service to handle the user requests in order to demonestrate how OpenTelemetry works!")

	shutdown := initProvider()
	defer shutdown()
	
	tracer = otel.Tracer("backend-tracer")
	meter := global.Meter("backend-meter")

	method, _ := baggage.NewMember("method", "repl")
	client, _ := baggage.NewMember("client", "cli")
	bag, _ := baggage.New(method, client)

	// labels represent additional key-value descriptors that can be bound to a
	// metric observer or recorder.
	// TODO: Use baggage when supported to extract labels from baggage.
	commonLabels := []attribute.KeyValue{
		attribute.String("app", "backend"),
	}

	// Recorder metric example
	requestLatency := metric.Must(meter).
		NewFloat64Histogram(
			"backend/request_latency",
			metric.WithDescription("The latency of requests processed"),
		)

	// TODO: Use a view to just count number of measurements for requestLatency when available.
	requestCount := metric.Must(meter).
		NewInt64Counter(
			"backend/request_counts",
			metric.WithDescription("The number of requests processed"),
		)

	checkoutHandler := func(w http.ResponseWriter, req *http.Request) {
		logger.Print("New checkout request received.")

		startTime := time.Now()

		ctx := req.Context()
		ctx = baggage.ContextWithBaggage(ctx, bag)

		// otelhttp already started a new span for handle function so you may need just get the span and add some events as needed
		span := trace.SpanFromContext(ctx)
		traceId := span.SpanContext().TraceID().String()
		logger.Printf("Handle request with trace id: %+v\n", traceId)

		// bag := baggage.FromContext(ctx)
		// ctx, span := tracer.Start(ctx, "checkout-handler")
		// defer span.End()

		var order Order
		err := json.NewDecoder(req.Body).Decode(&order)
		if err != nil {
			span.AddEvent("Error decoding order json", trace.WithAttributes(attribute.Key("err").String(err.Error())))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		logger.Printf("New Checkout received: %+v\n", order)

		payment(ctx, order)

		// ** Parallel operations
		ch1 := shipping(ctx, order)
		ch2 := invoice(ctx, order.Basket, order.Payment)
		<-ch1
		<-ch2
		// ***********************

		_, _ = io.WriteString(w, fmt.Sprintf("{\"trace-id\": \"%v\"}\n", traceId))

		latencyMs := float64(time.Since(startTime)) / 1e6

		meter.RecordBatch(
			ctx,
			commonLabels,
			requestLatency.Measurement(latencyMs),
			requestCount.Measurement(1),
		)

	}

	otelHandler := otelhttp.NewHandler(http.HandlerFunc(checkoutHandler), "handle-checkout")
	http.Handle("/checkout", otelHandler)

	logger.Printf("Listening on port 80\n")
	http.ListenAndServe(":80", nil)
}

func payment(ctx context.Context, order Order) {
	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	// we're ignoring errors here since we know these values are valid,
	// but do handle them appropriately if dealing with user-input
	// foo, _ := baggage.NewMember("ex.com.foo", "foo1")
	// bar, _ := baggage.NewMember("ex.com.bar", "bar1")
	// bag, _ := baggage.New(foo, bar)
	// ctx = baggage.ContextWithBaggage(ctx, bag)

	payload := fmt.Sprintf("{\"name\":\"%s\", \"amount\":%d, \"method\":\"%s\"}", order.Name, 12 /*calcAmount(ctx, order.Basket)*/, order.Payment)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://payment-gateway/", bytes.NewBuffer([]byte(payload)))

	res, err := httpClient.Do(req)

	span := trace.SpanFromContext(ctx)

	if err != nil {
		span.AddEvent("Error sending request", trace.WithAttributes(attribute.Key("err").String(err.Error())))
		return
	}

	defer res.Body.Close()

	if res.StatusCode == 200 {
		span.AddEvent("Successfully payment handeled")
	} else {
		span.AddEvent("Error Payment Gateway", trace.WithAttributes(attribute.Key("status").Int(res.StatusCode)))
	}
}

func shipping(ctx context.Context, order Order) <-chan bool {
	r := make(chan bool)

	go func() {
		httpClient := &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
		payload := fmt.Sprintf("{\"address\":\"%s\", \"vendor\":\"%s\", \"basket\":[\"%s\"]}", order.Address, order.Shipping, strings.Join(order.Basket, "\",\""))
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://shipping-gateway/", bytes.NewBuffer([]byte(payload)))

		res, err := httpClient.Do(req)

		span := trace.SpanFromContext(ctx)

		if err != nil {
			span.AddEvent("Error sending request", trace.WithAttributes(attribute.Key("err").String(err.Error())))
			r <- false
		} else {

			defer res.Body.Close()

			if res.StatusCode == 200 {
				span.AddEvent("Successfully shipping handeled")
			} else {
				span.AddEvent("Error Shipping Gateway", trace.WithAttributes(attribute.Key("status").Int(res.StatusCode)))
			}

			r <- true
		}
	}()

	return r
}

func invoice(ctx context.Context, basket []string, payment string) <-chan bool {
	r := make(chan bool)

	go func() {
		_, span := tracer.Start(ctx, "generating-invoice")
		defer span.End()

		span.AddEvent("Start generating invoice")

		<-time.After(60 * time.Millisecond)
		logger.Printf("Basket is %v\n", basket)

		span.AddEvent("Successfully invoice generated")
		r <- true
	}()

	return r
}

func calcAmount(ctx context.Context, basket []string) int {
	// You can create child spans from the span in the current context.
	// The returned context contains the new child span
	ctx, span := tracer.Start(ctx, "calculate-price")
	// Always end the span when the operation completes,
	// otherwise you will have a leak.
	defer span.End()

	// The new context now contains the child span, so it can be accessed
	// in other functions simply by passing the context.
	//    span := trace.SpanFromContext(ctx)

	span.AddEvent("Start calculating total price")

	<-time.After(6 * time.Millisecond)
	total := len(basket) * rand.Intn(500)
	logger.Printf("Total price is %v\n", total)

	span.SetAttributes(attribute.Int("total-price", total))
	span.AddEvent("Successfully total price calculated")

	return total
}

