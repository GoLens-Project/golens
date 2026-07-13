package golens

import (
	"io"
	"log"
	"net/http"
)

// redirectLog swaps the standard logger output for the duration of a test so
// assertions can inspect debug output.
func redirectLog(t TestingT, w io.Writer) {
	t.Helper()
	log.SetOutput(w)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
}

// newServer returns an http.Handler mounting the GoLens UI for tests.
func newServer(r *Registry) http.Handler {
	mux := http.NewServeMux()
	r.MountUI(mux)
	return mux
}

// TestingT is a minimal subset of *testing.T for helper signatures.
type TestingT interface {
	Helper()
	Cleanup(func())
}
