// Command gin is an example of mounting GoLens on a Gin (gin-gonic/gin)
// server. Run it and open http://localhost:8080/metrics.
//
// GoLens middleware is a standard func(http.Handler) http.Handler. Gin's
// router satisfies http.Handler via engine.ServeHTTP, so we wrap the engine
// with the GoLens middleware and feed the result back to a net/http.Server.
// This keeps the full RED pipeline (count, errors, latency) working without a
// Gin-specific adapter.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	golens "golens"
)

func main() {
	cfg := golens.DefaultConfig()
	cfg.Debug = os.Getenv("GOLENS_DEBUG") == "true"
	if path := os.Getenv("GOLENS_CONFIG"); path != "" {
		if loaded, err := golens.LoadConfig(path); err == nil {
			cfg = loaded
		}
	}

	registry, err := golens.New(cfg)
	if err != nil {
		log.Fatalf("golens: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := registry.Start(ctx); err != nil {
		log.Fatalf("start: %v", err)
	}
	defer registry.Close()

	// Custom domain metric via the fluent hook API. The returned middleware is
	// a standard http.Handler wrapper; we mount it on the Gin route by handing
	// the request off to a small http.HandlerFunc that calls back into Gin.
	orderHook := registry.On("orders_created").
		Type(golens.CounterType).
		Description("orders created").
		Labels("sku").
		Extract(func(req *http.Request) (float64, []golens.Label) {
			return 1, []golens.Label{{Name: "sku", Value: req.URL.Query().Get("sku")}}
		})

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Mount the GoLens dashboard at /metrics on Gin's router. The UI handlers
	// are plain net/http handlers; wrap them so Gin dispatches to them.
	mountHTTPHandler(r, "/metrics", registry.MetricsHTTPHandler())
	mountHTTPHandler(r, "/metrics/data", registry.MetricsDataHTTPHandler())
	mountHTTPHandler(r, "/metrics/endpoints", registry.EndpointsHTTPHandler())

	r.GET("/", func(c *gin.Context) {
		time.Sleep(5 * time.Millisecond) // simulate work
		if c.Query("fail") == "1" {
			c.String(http.StatusInternalServerError, "boom")
			return
		}
		c.String(http.StatusOK, "hello from gin\n")
	})

	// Apply the fluent-hook middleware to a single route. orderHook returns a
	// standard func(http.Handler) http.Handler; wrap the final handler with
	// http.HandlerFunc and adapt the result for Gin with gin.WrapH.
	r.GET("/order", gin.WrapH(orderHook(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))))

	// Wrap the whole Gin engine with the GoLens RED middleware. This is the
	// single entry point that records request count, errors, and duration.
	srv := &http.Server{Addr: ":8080", Handler: registry.Middleware(r)}
	go func() {
		log.Println("GoLens (gin) listening on :8080 (dashboard at /metrics)")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// mountHTTPHandler registers a net/http handler on a Gin route. The dashboard
// is a single page at /metrics that polls /metrics/data; no wildcard sub-paths
// are needed (and registering /metrics/*path would collide with /metrics/data).
func mountHTTPHandler(r *gin.Engine, path string, h http.Handler) {
	if h == nil {
		return
	}
	r.Any(path, gin.WrapH(h))
}
