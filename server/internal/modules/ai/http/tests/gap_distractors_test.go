package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/ai/application"
	aihttp "quizzivy/internal/modules/ai/http"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
)

func TestWrongOptionsForAGapAnswer501WhileTheAssistantIsOffOrHasNoProvider(t *testing.T) {
	for name, enabled := range map[string]bool{"turned off": false, "no provider": true} {
		t.Run(name, func(t *testing.T) {
			app := application.New(store{}, application.Options{People: nobodyHoldsMore{}})
			transport := aihttp.NewAI(app, enabled)
			teacher := uuid.NewString()
			handler := httpx.WithRequestMeta(func(*http.Request) string { return "203.0.113.9" })(
				httpx.RequireAuth(nil, func(string) (httpx.Principal, error) {
					return httpx.Principal{UserID: teacher, Access: access.Principal{UserID: teacher, Permissions: access.NewSet(access.AIUse)}}, nil
				})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					out, err := transport.SuggestAiGapDistractors(r.Context(), openapi.SuggestAiGapDistractorsRequestObject{
						Body: &openapi.SuggestAiGapDistractorsJSONRequestBody{Before: "Parks give people a place to ", After: ".", Answers: []string{"relax"}},
					})
					if err != nil {
						t.Fatal(err)
					}
					if err := out.VisitSuggestAiGapDistractorsResponse(w); err != nil {
						t.Fatal(err)
					}
				})))
			request := httptest.NewRequest(http.MethodPost, "/teacher/ai/gaps/distractors", nil)
			request.Header.Set("Authorization", "Bearer fixture")
			request.Header.Set("Accept-Language", "en")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			code, message, _ := envelope(t, response)
			if response.Code != http.StatusNotImplemented || code != "AI_OFF" || message != "The AI assistant is not turned on on this server." {
				t.Errorf("answered %d %s %q, want 501 AI_OFF", response.Code, code, message)
			}
		})
	}
}
