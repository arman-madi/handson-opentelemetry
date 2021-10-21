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

// initTracer creates a new trace provider instance and registers it as global trace provider.
func initTracer(url string) func() {
	stdoutExporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		// log.Panicf("failed to initialize stdouttrace exporter %v\n", err)
		log.Fatal(err)
	}

	// Create Zipkin Exporter and install it as a global tracer.
	//
	// For demoing purposes, always sample. In a production application, you should
	// configure the sampler to a trace.ParentBased(trace.TraceIDRatioBased) set at the desired
	// ratio.
	zipkinExporter, err := zipkin.New(
		url,
		zipkin.WithLogger(logger),
	)
	if err != nil {
		log.Fatal(err)
	}

	jaegerUrl := "http://jaeger-tracing:14268/api/traces"
	jaegerExporter, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(jaegerUrl)))
	if err != nil {
		log.Fatal(err)
	}


	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(zipkinExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithBatcher(jaegerExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithBatcher(stdoutExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("payment-gateway"),
		)),
	)
	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(propagation.TraceContext{})

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func main() {
	logger.Println("Hello Payer!")

	shutdown := initTracer("http://zipkin-collector:9411/api/v2/spans")
	defer shutdown()

	paymentHandler := func(w http.ResponseWriter, req *http.Request) {

		logger.Printf("HEADERS: %+v", req.Header)
		ctx := req.Context()
		logger.Printf("CONTEXT: %+v", ctx)

		logger.Printf("CTX: %+v \n", ctx)
		span := trace.SpanFromContext(ctx)
		// bag := baggage.FromContext(ctx)

		var payment Payment
		err := json.NewDecoder(req.Body).Decode(&payment)
		if err != nil {
			span.AddEvent("Error decoding payment json", trace.WithAttributes(attribute.Key("err").String(err.Error())))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		logger.Printf("New request received: %+v\n", payment)

		send(ctx, payment)

		_, _ = io.WriteString(w, "Goodbye payer!\n")
	}

	otelHandler := otelhttp.NewHandler(http.HandlerFunc(paymentHandler), "payment-handler")

	http.Handle("/", otelHandler)
	logger.Printf("Listening on port 80\n")
	http.ListenAndServe(":80", nil)
}

func send(ctx context.Context, payment Payment) {	
	// httpClient := &http.Client{
	// 	Transport: otelhttp.NewTransport(http.DefaultTransport),
	// }
	// tracer := otel.Tracer("handson-opentelemetry/payment-gateway")
	// _, span := tracer.Start(context.Background(), "makeRequest")
	// defer span.End()

	// payload := fmt.Sprintf("{\"name\":\"%s\", \"amount\":%d, \"method\":\"%s\"}", order.Name, calcAmount(ctx, order.Basket), order.Payment)
	// req, _ := http.NewRequestWithContext(ctx, "POST", "http://payment-gateway/", bytes.NewBuffer([]byte(payload)))
	// // req, _ := http.NewRequest("POST", "http://payment-gateway/", bytes.NewBuffer([]byte(payload)))

	// res, err := httpClient.Do(req)
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }
	// defer res.Body.Close()
	// fmt.Printf("Request to %s, got %d bytes\n", "http://payment-gateway/", res.ContentLength)




	// span := trace.SpanFromContext(ctx)
	tracer := otel.Tracer("handson-opentelemetry/payment-gateway")
	_, span := tracer.Start(ctx, "makeRequest")
	defer span.End()

	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	payload := fmt.Sprintf("{\"name\":\"%s\", \"amount\":%d}", payment.Name, payment.Amount)
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://%s/", payment.Method), bytes.NewBuffer([]byte(payload)))

	logger.Printf("Sending request to %s ...\n", payment.Method)
	res, err := client.Do(req)
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

