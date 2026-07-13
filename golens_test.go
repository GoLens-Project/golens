package golens

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerMountsUIAndMiddleware(t *testing.T) {
	cfg := DefaultConfig()
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(204)
	})
	r, h := Handler(cfg, inner)
	defer r.Close()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Errorf("UI status = %d", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/", nil))
	if rec2.Code != 204 {
		t.Errorf("inner handler status = %d", rec2.Code)
	}
}

func TestHandlerFallsBackOnBadConfig(t *testing.T) {
	// Point sqlite at an unwritable path to force New to fail inside Handler.
	cfg := DefaultConfig()
	cfg.Storage.Backend = "sqlite"
	cfg.Storage.Path = "/nonexistent-dir/cannot-create.db"
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) })
	r, h := Handler(cfg, inner)
	defer r.Close()
	// Handler must still serve requests via the fallback registry.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Errorf("fallback status = %d", rec.Code)
	}
}

func TestVersionConstant(t *testing.T) {
	if Version == "" {
		t.Error("Version not set")
	}
}
