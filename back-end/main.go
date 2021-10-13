package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
			semconv.ServiceNameKey.String("back-end"),
		)),
	)
	otel.SetTracerProvider(tp)

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func main() {
	logger.Println("Hello Shoppers!")


	shutdown := initTracer("http://zipkin-collector:9411/api/v2/spans")
	defer shutdown()


	http.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {

		ctx := context.Background()
		tr := otel.GetTracerProvider().Tracer("order")

		ctx, span := tr.Start(ctx, "checkout", trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		var order Order
		err := json.NewDecoder(r.Body).Decode(&order)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		logger.Printf("New Checkout received: %+v\n", order)
		
		payment(ctx, tr, order)
		shipping(ctx, tr, order.Shipping)
		invoice(ctx, tr, order.Basket, order.Payment)

		body := []byte("Success!")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})

	logger.Printf("Listening on port 80\n")
	http.ListenAndServe(":80", nil)
}

func payment(ctx context.Context, tr trace.Tracer, order Order) {	
	_, span := tr.Start(ctx, "payment", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	client := http.Client{Transport: http.DefaultTransport}
	payload := fmt.Sprintf("{\"name\":\"%s\", \"amount\":%d, \"method\":\"%s\"}", order.Name, calcAmount(ctx, tr, order.Basket), order.Payment)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://payment-gateway/", bytes.NewBuffer([]byte(payload)))
	logger.Println("Sending request to payment gateway ...")
	res, err := client.Do(req)
	if err != nil {
		span.AddEvent("Error sending request", trace.WithAttributes(attribute.Key("err").String(err.Error())))
		return 
	}

	if res.StatusCode == 200 {
		span.AddEvent("Successfully payment handeled")
	} else {
		span.AddEvent("Error Payment Gateway", trace.WithAttributes(attribute.Key("status").Int(res.StatusCode)))
	}
	
}

func shipping(ctx context.Context, tr trace.Tracer, shipping string) {
	_, span := tr.Start(ctx, "shipping", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()
	<-time.After(33 * time.Millisecond)
	logger.Printf("Shipping is %s\n", shipping)
}

func invoice(ctx context.Context, tr trace.Tracer, basket []string, payment string) {
	// This is a new root span
	ctx, span := tr.Start(ctx, "invoice", trace.WithSpanKind(trace.SpanKindServer), trace.WithNewRoot())
	defer span.End()
	<-time.After(6 * time.Millisecond)
	logger.Printf("Basket is %v\n", basket)
}

func calcAmount(ctx context.Context, tr trace.Tracer, basket []string) int{
	_, span := tr.Start(ctx, "calc", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	<-time.After(6 * time.Millisecond)
	logger.Printf("Basket is %v\n", basket)
	return len(basket) * 10
}