package httpx

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

type rawBodyKey struct{}

// KeepRawBody hands the operations in routes their request body byte for
// byte, through RawBodyFromContext, for a handler that has to check a
// signature over it: the generated handler only sees the decoded JSON. It
// runs after LimitRequestBody, which has already read the body within its
// budget, and puts the same bytes back for the validator and the decoder.
// Every other operation passes through untouched.
func KeepRawBody(routes map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, keep := routes[r.Pattern]; !keep || r.Body == nil {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				WriteError(w, r, http.StatusRequestTimeout, CodeRequestIncomplete,
					TextFor(r, "Máy chủ chưa nhận đủ dữ liệu gửi lên. Vui lòng thử lại.", "The server did not receive the whole request. Try again."))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawBodyKey{}, body)))
		})
	}
}

// RawBodyFromContext is the body KeepRawBody kept for this request, and false
// for an operation it does not keep one for.
func RawBodyFromContext(ctx context.Context) ([]byte, bool) {
	body, ok := ctx.Value(rawBodyKey{}).([]byte)
	return body, ok
}
