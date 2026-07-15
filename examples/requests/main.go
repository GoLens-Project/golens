// Command requests drives the example API endpoints to generate telemetry for
// the GoLens dashboard. It has two modes:
//
//	burst — 3-5 requests per endpoint, back-to-back (no delay)
//	load  — requests for 30s, with a random 500-1000ms delay between each
//
// Usage:
//
//	go run examples/requests/main.go            # default: burst
//	go run examples/requests/main.go burst
//	go run examples/requests/main.go load
//
// Set GOLENS_ADDR to point at a different host (default http://localhost:8080).
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"
)

// endpoint is a target path on the example server. Some intentionally produce
// errors (fail=1) so the dashboard and OTLP export see non-zero error counts.
var endpoints = []string{
	"/",
	"/?fail=1",
	"/order?sku=ABC",
	"/order?sku=PRO",
	"/connections",
	"/size",
	"/cpu",
	"/latency",
	"/conn-distribution",
}

func main() {
	mode := flag.String("mode", "burst", "mode: burst or load")
	flag.Parse()

	addr := os.Getenv("GOLENS_ADDR")
	if addr == "" {
		addr = "http://localhost:8080"
	}

	// Preflight: fail fast with a helpful message instead of hammering a dead
	// server for the whole run.
	if !reachable(addr) {
		log.Fatalf("cannot reach %s — is the example server running?\n"+
			"  start one with:  make run-stdlib   (or:  make run-gin)", addr)
	}

	// Fixed seed for reproducibility within a run; swap for rand.New(rand.NewSource(...))
	// if you want per-run variance.
	rng := rand.New(rand.NewSource(1))

	switch *mode {
	case "burst":
		runBurst(addr, rng)
	case "load":
		runLoad(addr, rng)
	default:
		log.Fatalf("unknown mode %q (want burst or load)", *mode)
	}
}

// reachable reports whether the server responds to a single request. Any HTTP
// response (even an error status) counts as reachable; only network failures
// (connection refused, DNS, timeout) return false.
func reachable(addr string) bool {
	resp, err := http.Get(addr + "/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// runBurst makes 3-5 requests per endpoint with no delay between them.
func runBurst(addr string, rng *rand.Rand) {
	total, ok := 0, 0
	for _, ep := range endpoints {
		n := 3 + rng.Intn(3) // 3..5
		for i := 0; i < n; i++ {
			status := request(addr, ep)
			total++
			if status < 400 {
				ok++
			}
			fmt.Printf("burst  %-18s -> %d\n", ep, status)
		}
	}
	fmt.Printf("\nburst done: %d requests, %d ok, %d errors\n", total, ok, total-ok)
}

// runLoad sends requests for 30s, sleeping a random 500-1000ms between each.
func runLoad(addr string, rng *rand.Rand) {
	const duration = 30 * time.Second
	deadline := time.Now().Add(duration)
	total, ok := 0, 0

	for time.Now().Before(deadline) {
		ep := endpoints[rng.Intn(len(endpoints))]
		status := request(addr, ep)
		total++
		if status < 400 {
			ok++
		}
		remaining := time.Until(deadline).Truncate(time.Second)
		fmt.Printf("load  %-18s -> %d  (remaining %v)\n", ep, status, remaining)

		delay := time.Duration(500+rng.Intn(501)) * time.Millisecond // 500..1000ms
		time.Sleep(delay)
	}
	fmt.Printf("\nload done: %d requests, %d ok, %d errors over %v\n", total, ok, total-ok, duration)
}

// request issues a single GET and returns the HTTP status code. Network errors
// are reported as 0 so the caller can keep running.
func request(addr, path string) int {
	resp, err := http.Get(addr + path)
	if err != nil {
		fmt.Printf("  ! request error: %v\n", err)
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}
