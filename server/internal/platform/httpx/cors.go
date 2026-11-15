package httpx

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// PublicOrigin is an origin that is not one of the web hosts, and the
// operations its pages may call without credentials: each as "METHOD /path",
// the path written out with no wildcard. An empty Origin admits nothing.
type PublicOrigin struct {
	Origin     string
	Operations []string
}

func (p PublicOrigin) serves(origin, method, path string) bool {
	return p.Origin != "" && p.Origin == origin && slices.Contains(p.Operations, method+" "+path)
}

// CORS admits the web hosts in allowedOrigins, exactly as they are written,
// to the API on another host of the same site, with credentials. Each of
// public is admitted to its own operations and their preflights only, and
// never with credentials: the answer names the origin, exposes X-Request-Id
// and Retry-After, and allows the one method and the Content-Type header.
// Every other request from such an origin is answered as one from an origin
// nobody listed.
func CORS(allowedOrigins []string, public ...PublicOrigin) func(http.Handler) http.Handler {
	policy := corsPolicy{public: public}
	for _, o := range allowedOrigins {
		if trimmed := strings.TrimSpace(o); trimmed != "" && trimmed != "*" {
			policy.allowed = append(policy.allowed, trimmed)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if policy.answer(w, r) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type corsPolicy struct {
	allowed []string
	public  []PublicOrigin
}

func (p corsPolicy) answer(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	preflight := isPreflight(r)
	method := r.Method
	if preflight {
		method = r.Header.Get("Access-Control-Request-Method")
	}

	w.Header().Add("Vary", "Origin")
	switch {
	case origin != "" && slices.Contains(p.allowed, origin):
		allowOrigin(w, origin)
		if preflight {
			writePreflight(w, true)
		}
	case servedPublicly(p.public, origin, method, r.URL.Path):
		allowPublicOrigin(w, origin)
		if preflight {
			writePublicPreflight(w, method)
		}
	case preflight:
		writePreflight(w, false)
	}
	return preflight
}

func servedPublicly(public []PublicOrigin, origin, method, path string) bool {
	return slices.ContainsFunc(public, func(p PublicOrigin) bool { return p.serves(origin, method, path) })
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func allowOrigin(w http.ResponseWriter, origin string) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Set("Access-Control-Expose-Headers", "X-Request-Id, Retry-After, Content-Disposition")
}

func allowPublicOrigin(w http.ResponseWriter, origin string) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Expose-Headers", "X-Request-Id, Retry-After")
}

const preflightMaxAge = 600

func varyOnPreflight(w http.ResponseWriter) {
	h := w.Header()
	h.Add("Vary", "Access-Control-Request-Method")
	h.Add("Vary", "Access-Control-Request-Headers")
}

func writePreflight(w http.ResponseWriter, permitted bool) {
	varyOnPreflight(w)
	if permitted {
		h := w.Header()
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
		h.Set("Access-Control-Max-Age", strconv.Itoa(preflightMaxAge))
	}
	w.WriteHeader(http.StatusNoContent)
}

func writePublicPreflight(w http.ResponseWriter, method string) {
	varyOnPreflight(w)
	h := w.Header()
	h.Set("Access-Control-Allow-Methods", method)
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Max-Age", strconv.Itoa(preflightMaxAge))
	w.WriteHeader(http.StatusNoContent)
}
