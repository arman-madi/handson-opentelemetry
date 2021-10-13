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
	"go.opentelemetry.io/otel/exporters/zipkin"
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
	// Create Zipkin Exporter and install it as a global tracer.
	//
	// For demoing purposes, always sample. In a production application, you should
	// configure the sampler to a trace.ParentBased(trace.TraceIDRatioBased) set at the desired
	// ratio.
	exporter, err := zipkin.New(
		url,
		zipkin.WithLogger(logger),
	)
	if err != nil {
		log.Fatal(err)
	}

	batcher := sdktrace.NewBatchSpanProcessor(exporter)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(batcher),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("payment-gateway"),
		)),
	)
	otel.SetTracerProvider(tp)

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func main() {
	logger.Println("Hello Payer!")

	shutdown := initTracer("http://zipkin-collector:9411/api/v2/spans")
	defer shutdown()

	paymentHandler := func(w http.ResponseWriter, req *http.Request) {

		ctx := req.Context()
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

	otelHandler := otelhttp.NewHandler(http.HandlerFunc(paymentHandler), "Payment")

	http.Handle("/", otelHandler)
	logger.Printf("Listening on port 80\n")
	http.ListenAndServe(":80", nil)
}

func send(ctx context.Context, payment Payment) {	
	span := trace.SpanFromContext(ctx)
	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
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

