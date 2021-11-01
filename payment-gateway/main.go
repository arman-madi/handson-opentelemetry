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

type Payment struct {
	Name string `json:"name"`
	Method string `json:"method"`
	Amount int `json:"amount"` 
}


var logger = log.New(os.Stderr, "[payment-gateway] ", log.Ldate|log.Ltime|log.Llongfile)

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
			semconv.ServiceNameKey.String("payment-gateway"),
			attribute.String("environment", "demo"),
			attribute.Int64("ID", 2),
		)),
	)

	// Register our TracerProvider as the global so any imported
	// instrumentation in the future will default to using it.
	otel.SetTracerProvider(tp)

	// Register the TraceContext propagator globally.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	// otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	
	// Name the tracer after the package, or the service if you are in main
	tracer = otel.Tracer("handson-opentelemetry/payment-gateway")

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func main() {
	logger.Println("Hello, this is payment-gateway service which is responsible to dispatch user payment requests in order to demonestrate how OpenTelemetry works!")

	flush := initTracer()
	defer flush()

	paymentHandler := func(w http.ResponseWriter, req *http.Request) {

		ctx := req.Context()
		span := trace.SpanFromContext(ctx)
		traceId := span.SpanContext().TraceID().String()
		logger.Printf("Handle request with trace id: %+v\n", traceId)

		var payment Payment
		err := json.NewDecoder(req.Body).Decode(&payment)
		if err != nil {
			span.AddEvent("Error decoding payment json", trace.WithAttributes(attribute.Key("err").String(err.Error())))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		logger.Printf("New request received: %+v\n", payment)

		send(ctx, payment)

		_, _ = io.WriteString(w, fmt.Sprintf("{\"trace-id\": \"%v\"}\n", traceId))
	}

	otelHandler := otelhttp.NewHandler(http.HandlerFunc(paymentHandler), "handle-payment")

	http.Handle("/", otelHandler)
	logger.Printf("Listening on port 80\n")
	http.ListenAndServe(":80", nil)
}

func send(ctx context.Context, payment Payment) {	
	client := http.DefaultClient

	payload := fmt.Sprintf("{\"name\":\"%s\", \"amount\":%d}", payment.Name, payment.Amount)
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/", payment.Method), bytes.NewBuffer([]byte(payload)))

	_, req = otelhttptrace.W3C(ctx, req)
	otelhttptrace.Inject(ctx, req, 
		// It seems otelhttptrace.W3C didn't consider global propagator, so you must explecitly inject
		otelhttptrace.WithPropagators(propagation.TraceContext{}),
	)
	
	logger.Printf("Sending request to %s with headers %+v ...\n", payment.Method, req.Header)
	res, err :=client.Do(req)

	span := trace.SpanFromContext(ctx)
	
	if err != nil {
		span.AddEvent(fmt.Sprintf("Error sending %s request", payment.Method), trace.WithAttributes(attribute.Key("err").String(err.Error())))
		return 
	}

	if res.StatusCode == 200 {
		span.AddEvent("Successfully paid", trace.WithAttributes(attribute.Key("payment-method").String(payment.Method)))
	} else {
		span.AddEvent(fmt.Sprintf("Error paying with %s", payment.Method), trace.WithAttributes(attribute.Key("status").Int(res.StatusCode)))
	}
}

