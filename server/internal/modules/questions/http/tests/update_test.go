package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/questions/application"
	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/domain"
	questionshttp "quizzivy/internal/modules/questions/http"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

func TestUpdatingAQuestionTheCallerCannotReachAnswers404(t *testing.T) {
	app := &application.Application{Commands: application.Commands{Update: cqrs.HandlerFunc[command.Update, domain.Question](func(context.Context, command.Update) (domain.Question, error) {
		return domain.Question{}, domain.ErrNotFound
	})}}
	principal := access.Principal{UserID: uuid.NewString(), Permissions: access.NewSet(access.ContentQuestionsWrite)}
	response, err := questionshttp.NewQuestions(app, nil).UpdateQuestion(contextAs(t, principal),
		openapi.UpdateQuestionRequestObject{Id: uuid.New(), Body: questionBody[openapi.UpdateQuestionJSONRequestBody](t)})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := response.VisitUpdateQuestionResponse(rec); err != nil {
		t.Fatal(err)
	}
	var problem openapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || problem.Error.Code != openapi.NOTFOUND {
		t.Errorf("an unreachable question answered %d %s, want the contract's 404 NOT_FOUND", rec.Code, problem.Error.Code)
	}
}

// withExpectedUpdatedAt adds the updatedAt an editor read to a question body,
// which updateQuestion requires since R8.
func withExpectedUpdatedAt(t *testing.T, raw []byte) []byte {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	body["expectedUpdatedAt"] = "2026-11-23T08:00:00.123456Z"
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAnUpdateWithoutTheRevisionItReadIsRefusedByTheContract is T-R8.1:
// updateQuestion's expectedUpdatedAt is required, so httpx.ValidateRequests
// answers 400 VALIDATION_FAILED naming it before the handler runs, while
// createQuestion and a test's editTestQuestion, where it stays optional,
// still reach theirs.
func TestAnUpdateWithoutTheRevisionItReadIsRefusedByTheContract(t *testing.T) {
	spec, err := openapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	validate, err := httpx.ValidateRequests(spec)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"type":"short_answer","prompt":"Câu hỏi","points":1,"tags":[]}`)
	for _, c := range []struct {
		name, method, path string
		body               []byte
		reaches            bool
	}{
		{"an update without it", http.MethodPatch, "/teacher/questions/" + uuid.NewString(), plain, false},
		{"an update with a null", http.MethodPatch, "/teacher/questions/" + uuid.NewString(), []byte(`{"type":"short_answer","prompt":"Câu hỏi","points":1,"expectedUpdatedAt":null}`), false},
		{"an update with it", http.MethodPatch, "/teacher/questions/" + uuid.NewString(), withExpectedUpdatedAt(t, plain), true},
		{"a create without it", http.MethodPost, "/teacher/questions", plain, true},
		{"a test's edit without it", http.MethodPatch, "/teacher/tests/" + uuid.NewString() + "/questions/" + uuid.NewString(), plain, true},
	} {
		reached := false
		handler := validate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true; w.WriteHeader(http.StatusNoContent) }))
		req := httptest.NewRequest(c.method, c.path, bytes.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if reached != c.reaches {
			t.Errorf("%s reached the handler %v, want %v (%d %s)", c.name, reached, c.reaches, rec.Code, rec.Body.String())
			continue
		}
		if c.reaches {
			continue
		}
		// The bare validator writes no request id, which ErrorResponse's uuid
		// would refuse, so the body is read as plain JSON.
		var problem struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusBadRequest || problem.Error.Code != string(openapi.VALIDATIONFAILED) || !strings.Contains(problem.Error.Message, "expectedUpdatedAt") {
			t.Errorf("%s answered %d %s %q, want 400 VALIDATION_FAILED naming expectedUpdatedAt", c.name, rec.Code, problem.Error.Code, problem.Error.Message)
		}
	}
}

// TestTheUpdateBodyReachesTheCommandWhole fails when a field updateQuestion's
// body carries is not copied into the QuestionInput the handler maps, as
// would happen when the contract adds one to QuestionInput and UpdateInput is
// not told: every field of the body is set, and every field of the input must
// arrive set.
func TestTheUpdateBodyReachesTheCommandWhole(t *testing.T) {
	var body openapi.UpdateQuestionJSONRequestBody
	fill(t, reflect.ValueOf(&body).Elem())
	in := reflect.ValueOf(questionshttp.UpdateInput(body))
	if got, want := in.NumField(), reflect.TypeOf(body).NumField(); got != want {
		t.Fatalf("QuestionInput has %d fields and updateQuestion's body %d; UpdateInput maps one onto the other", got, want)
	}
	for i := range in.NumField() {
		if in.Field(i).IsZero() {
			t.Errorf("UpdateInput drops %s", in.Type().Field(i).Name)
		}
	}
	if *questionshttp.UpdateInput(body).ExpectedUpdatedAt != body.ExpectedUpdatedAt {
		t.Error("UpdateInput changes the revision the editor read")
	}
}

// fill sets every field of v to a value that is not its zero.
func fill(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(t, v.Elem())
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)))
			return
		}
		for i := range v.NumField() {
			fill(t, v.Field(i))
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(t, v.Index(0))
	case reflect.Array:
		for i := range v.Len() {
			fill(t, v.Index(i))
		}
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	default:
		t.Fatalf("fill does not know %s", v.Type())
	}
}
