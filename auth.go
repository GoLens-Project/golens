package golens

import (
	"crypto/subtle"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// authState holds the resolved admin-auth credentials. It is populated once in
// New from UIConfig.Auth; the plaintext password is never retained — only its
// bcrypt hash. active() reports whether the built-in basic-auth wrapper should
// be applied.
type authState struct {
	username string
	hash     []byte // bcrypt hash of the admin password
}

func (a authState) active() bool { return a.username != "" && len(a.hash) > 0 }

// resolveAuth validates the declarative AuthConfig and returns the resolved
// authState. A plaintext password (optionally pulled from the environment via
// "env:VAR") is bcrypt-hashed here so it is not retained beyond load; the
// plaintext is also wiped from the supplied AuthConfig so the long-lived
// Registry.cfg never carries it.
//
// "env:" resolution is applied even when the YAML/embedded config supplies a
// literal value, so both `password: env:GOLENS_ADMIN_PASSWORD` and
// `password: s3cret` work. A pre-hashed password_hash is honored when no
// plaintext password is supplied.
func resolveAuth(a *AuthConfig) authState {
	a.Password = expandEnv(a.Password)

	user := a.Username
	var hash []byte

	switch {
	case a.Password != "":
		h, err := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
		if err != nil {
			return authState{} // invalid plaintext → auth stays inactive
		}
		hash = h
	case a.PasswordHash != "":
		hash = []byte(a.PasswordHash)
	}

	// Wipe the plaintext from the caller's config regardless of which path ran.
	a.Password = ""

	if user == "" || len(hash) == 0 {
		return authState{}
	}
	a.enabled = true
	return authState{username: user, hash: hash}
}

// basicAuth wraps an http.HandlerFunc with HTTP Basic Auth (RFC 7617). The
// username is compared in constant time; the password is verified against the
// stored bcrypt hash. On failure it responds 401 with a WWW-Authenticate
// challenge and does not invoke the wrapped handler.
func (r *Registry) basicAuth(next http.HandlerFunc) http.Handler {
	user := []byte(r.auth.username)
	hash := r.auth.hash
	realm := "GoLens admin"

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, p, ok := req.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), user) != 1 ||
			bcrypt.CompareHashAndPassword(hash, []byte(p)) != nil {

			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
			http.Error(w, "Unauthorized\n", http.StatusUnauthorized)
			return
		}
		next(w, req)
	})
}
