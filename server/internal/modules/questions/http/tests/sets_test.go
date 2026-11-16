package http_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/questions/application"
	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/application/query"
	"quizzivy/internal/modules/questions/domain"
	questionshttp "quizzivy/internal/modules/questions/http"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

// listQuestions opens to workspace.teacher, but a set's own operations need
// content.questions.write; a caller without it reads a set's questions as a
// set it cannot read, whatever grant it holds.
func TestASetsQuestionsNeedTheSetsPermission(t *testing.T) {
	listed := false
	app := &application.Application{Queries: application.Queries{List: cqrs.HandlerFunc[query.List, query.ListResult](
		func(context.Context, query.List) (query.ListResult, error) {
			listed = true
			return query.ListResult{}, domain.ErrSetNotFound
		})}}
	setID := uuid.New()
	grader := access.Principal{UserID: uuid.NewString(), Permissions: access.NewSet(access.WorkspaceTeacher, access.TeachingGrading)}
	got, err := questionshttp.NewQuestions(app, nil).ListQuestions(contextAs(t, grader), openapi.ListQuestionsRequestObject{
		Params: openapi.ListQuestionsParams{SetId: &setID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(openapi.ListQuestions404JSONResponse); !ok || listed {
		t.Fatalf("a caller without content.questions.write listing a set got %T (the list ran: %v), want 404 before the read", got, listed)
	}

	author := access.Principal{UserID: uuid.NewString(), Permissions: access.NewSet(access.WorkspaceTeacher, access.ContentQuestionsWrite)}
	got, err = questionshttp.NewQuestions(app, nil).ListQuestions(contextAs(t, author), openapi.ListQuestionsRequestObject{
		Params: openapi.ListQuestionsParams{SetId: &setID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(openapi.ListQuestions404JSONResponse); !ok || !listed {
		t.Fatalf("a caller with content.questions.write got %T (the list ran: %v), want the read to decide", got, listed)
	}
}

func TestASetsDescriptionIsClearedByNullAndKeptWhenAbsent(t *testing.T) {
	var got domain.SetFields
	app := &application.Application{Commands: application.Commands{UpdateSet: cqrs.HandlerFunc[command.UpdateSet, domain.QuestionSet](
		func(_ context.Context, cmd command.UpdateSet) (domain.QuestionSet, error) {
			got = cmd.Fields
			return domain.QuestionSet{Access: domain.AccessOwner}, nil
		})}}
	principal := access.Principal{UserID: uuid.NewString(), Permissions: access.NewSet(access.ContentQuestionsWrite)}
	name := "Bộ đã đổi tên"
	for label, c := range map[string]struct {
		description json.RawMessage
		set         bool
		want        *string
	}{
		"null clears it":     {json.RawMessage(`null`), true, nil},
		"a text sets it":     {json.RawMessage(`"Cho lớp tối"`), true, new("Cho lớp tối")},
		"no field leaves it": {nil, false, nil},
	} {
		got = domain.SetFields{}
		_, err := questionshttp.NewQuestions(app, nil).UpdateQuestionSet(contextAs(t, principal), openapi.UpdateQuestionSetRequestObject{
			Id: uuid.New(), Body: &openapi.UpdateQuestionSetJSONRequestBody{Name: &name, Description: c.description},
		})
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if got.DescriptionSet != c.set || (got.Description == nil) != (c.want == nil) || got.Description != nil && *got.Description != *c.want {
			t.Errorf("%s: the command got %+v", label, got)
		}
		if got.Name == nil || *got.Name != name {
			t.Errorf("%s: the name was lost: %v", label, got.Name)
		}
	}
}
