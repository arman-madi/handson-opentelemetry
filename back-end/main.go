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

	// "go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
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
			semconv.ServiceNameKey.String("back-end"),
			attribute.String("environment", "demo"),
			attribute.Int64("ID", 1),
		)),
	)

	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Register our TracerProvider as the global so any imported
	// instrumentation in the future will default to using it.
	otel.SetTracerProvider(tp)

	// Name the tracer after the package, or the service if you are in main
	tracer = otel.Tracer("handson-opentelemetry/back-end")

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func main() {
	logger.Println("Hello, this is back-end service which is first service to handle the user requests in order to demonestrate how OpenTelemetry works!")

	shutdown := initTracer()
	defer shutdown()

	checkoutHandler := func(w http.ResponseWriter, req *http.Request) {

		ctx := req.Context()

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
