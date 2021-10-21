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
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

type Order struct {
	Name string `json:"name"`
	Address string `json:"address"`
	Payment string `json:"payment"`
	Shipping string `json:"shipping"` 
	Basket []string `json:"basket"`
}


var logger = log.New(os.Stderr, "[back-end] ", log.Ldate|log.Ltime|log.Llongfile)

// Create one tracer per package
// NOTE: You only need a tracer if you are creating your own spans
var tracer trace.Tracer


// initTracer creates a new trace provider instance and registers it as global trace provider.
func initTracer() /*(*sdktrace.TracerProvider, error)*/  func() {

	// ** STDOUT Exporter
	stdoutExporter, err := stdouttrace.New(/*stdouttrace.WithPrettyPrint()*/)
	if err != nil {
		log.Fatal("failed to initialize stdouttrace exporter: ", err)
	}

	// ** Jaeger Exporter
	jaegerUrl := "http://jaeger-tracing:14268/api/traces"
	jaegerExporter, err := jaeger.New(
		jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(jaegerUrl)),
	)
	if err != nil {
		log.Fatal("failed to initialize jaeger exporter: ", err)
	}

	// ** Zipkin Exporter 
	zipkinUrl := "http://zipkin-collector:9411/api/v2/spans"
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
			attribute.String("environment", "production"),
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

	// return tp, nil
}

func main() {
	// time.Sleep(time.Second * (30))
	logger.Println("Hello Shoppers!")

	shutdown := initTracer()
	defer shutdown()

	// tp, err := initTracer()
	// if err != nil {
	// 	log.Fatal(err)
	// }


	// // Register our TracerProvider as the global so any imported
	// // instrumentation in the future will default to using it.
	// otel.SetTracerProvider(tp)

	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()

	// // Cleanly shutdown and flush telemetry when the application exits.
	// defer func(ctx context.Context) {
	// 	// Do not make the application hang when it is shutdown.
	// 	ctx, cancel = context.WithTimeout(ctx, time.Second*5)
	// 	defer cancel()
	// 	if err := tp.Shutdown(ctx); err != nil {
	// 		log.Fatal(err)
	// 	}
	// }(ctx)

	tracer = otel.Tracer("handson-opentelemetry/backend")


	checkoutHandler := func(w http.ResponseWriter, req *http.Request) {

		ctx := req.Context()
		span := trace.SpanFromContext(ctx)
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
		shipping(ctx, order.Shipping)
		invoice(ctx, order.Basket, order.Payment)

		_, _ = io.WriteString(w, "Goodbye Customer!\n")
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
	foo, _ := baggage.NewMember("ex.com.foo", "foo1")
	bar, _ := baggage.NewMember("ex.com.bar", "bar1")
	bag, _ := baggage.New(foo, bar)
	ctx = baggage.ContextWithBaggage(ctx, bag)


	payload := fmt.Sprintf("{\"name\":\"%s\", \"amount\":%d, \"method\":\"%s\"}", order.Name, calcAmount(ctx, order.Basket), order.Payment)
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

func shipping(ctx context.Context, shipping string) {

	_, span := tracer.Start(ctx, "shipping-request")
	defer span.End()


	<-time.After(33 * time.Millisecond)
	logger.Printf("Shipping is %s\n", shipping)
	span.AddEvent("Successfully shipping handeled")
}

func invoice(ctx context.Context, basket []string, payment string) {

	_, span := tracer.Start(ctx, "invoice-request")
	defer span.End()


	<-time.After(6 * time.Millisecond)
	logger.Printf("Basket is %v\n", basket)
	span.AddEvent("Successfully invoice handeled")
}

func calcAmount(ctx context.Context, basket []string) int{
   // You can create child spans from the span in the current context.
   // The returned context contains the new child span
   ctx, span := tracer.Start(ctx, "calculate-price")
   // Always end the span when the operation completes,
   // otherwise you will have a leak.
   defer span.End()

   // The new context now contains the child span, so it can be accessed
   // in other functions simply by passing the context.
   //    span := trace.SpanFromContext(ctx)

	<-time.After(6 * time.Millisecond)
	logger.Printf("Basket is %v\n", basket)
	span.AddEvent("Successfully calc handeled")
	return len(basket) * 10
}
