// Command stdlibmux is a minimal example of mounting GoLens on a stdlib
// net/http server. Run it and open http://localhost:8080/metrics.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	mux := http.NewServeMux()
	registry.MountUI(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// simulate work
		time.Sleep(5 * time.Millisecond)
		if r.URL.Query().Get("fail") == "1" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from your app\n"))
	})

	// Custom domain metric via fluent hook.
	orderHook := registry.On("orders_created").
		Type(golens.CounterType).
		Description("orders created").
		Labels("sku").
		Extract(func(req *http.Request) (float64, []golens.Label) {
			return 1, []golens.Label{{Name: "sku", Value: req.URL.Query().Get("sku")}}
		})
	mux.Handle("/order", orderHook(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})))

	srv := &http.Server{Addr: ":8080", Handler: registry.Middleware(mux)}
	go func() {
		log.Println("GoLens (stdlib mux) listening on :8080 (dashboard at /metrics)")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
