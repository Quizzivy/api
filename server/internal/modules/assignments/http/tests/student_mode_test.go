package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/assignments/application"
	"quizzivy/internal/modules/assignments/application/query"
	"quizzivy/internal/modules/assignments/domain"
	assignmentshttp "quizzivy/internal/modules/assignments/http"
	"quizzivy/internal/shared/cqrs"
	"quizzivy/internal/shared/schedule"
)

// The /app/* leak review of F1: the student's card, intro and session gained
// one field, mode, and nothing else; time on the paper (activeSeconds) is the
// teacher's alone.
func TestTheStudentPayloadsGainedTheModeAlone(t *testing.T) {
	for _, c := range []struct {
		of   any
		keys string
	}{
		{openapi.StudentAssignmentCard{}, "attemptsUsed classId classIds className closesAt durationMinutes hasLiveAttempt id lastAttemptId lastSubmittedAt liveAnsweredCount liveDeadlineAt maxAttempts mode opensAt questionCount score status testTitle totalPoints"},
		{openapi.StudentAssignmentDetail{}, "attemptsUsed audioMaxPlays className closesAt durationMinutes hasAudio hasLiveAttempt hasSharedAudio id integrity lastAttemptId lastSubmittedAt liveDeadlineAt maxAttempts mode opensAt questionCount review score showsTranscript status studentNote teacherName testTitle totalPoints"},
		{openapi.AttemptSession{}, "answers attempt audioPlays beaconToken groupAudioPlays groups integrity mode questions remainingAttempts sections serverTime sessionId superseded testTitle"},
	} {
		of := reflect.TypeOf(c.of)
		var keys []string
		for i := range of.NumField() {
			keys = append(keys, strings.Split(of.Field(i).Tag.Get("json"), ",")[0])
		}
		slices.Sort(keys)
		if got := strings.Join(keys, " "); got != c.keys {
			t.Errorf("%s carries\n  %s\nwant\n  %s", of.Name(), got, c.keys)
		}
	}
}

func TestTheStudentsCardAndIntroNameTheMode(t *testing.T) {
	card := domain.StudentCard{ID: uuid.NewString(), Mode: schedule.Flexible}
	strict := domain.StudentCard{ID: uuid.NewString()}
	transport := assignmentshttp.NewAssignments(&application.Application{Queries: application.Queries{
		ForStudent: cqrs.HandlerFunc[query.ForStudent, domain.StudentSections](func(context.Context, query.ForStudent) (domain.StudentSections, error) {
			return domain.StudentSections{DueNow: []domain.StudentCard{card, strict}}, nil
		}),
		StudentDetail: cqrs.HandlerFunc[query.StudentDetail, domain.StudentDetail](func(context.Context, query.StudentDetail) (domain.StudentDetail, error) {
			return domain.StudentDetail{StudentCard: card}, nil
		}),
	}})
	var list openapi.ListMyAssignmentsResponseObject
	var intro openapi.GetMyAssignmentResponseObject
	refusedIn(t, "", func(ctx context.Context, _ http.ResponseWriter) error {
		var err error
		if list, err = transport.ListMyAssignments(ctx, openapi.ListMyAssignmentsRequestObject{}); err != nil {
			return err
		}
		intro, err = transport.GetMyAssignment(ctx, openapi.GetMyAssignmentRequestObject{Id: uuid.MustParse(card.ID)})
		return err
	})
	due := list.(openapi.ListMyAssignments200JSONResponse).DueNow
	if len(due) != 2 || due[0].Mode != openapi.Flexible || due[1].Mode != openapi.Strict {
		t.Errorf("cards %+v, want flexible then strict (a mode never read reads strict)", due)
	}
	body, err := json.Marshal(intro)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"mode":"flexible"`) {
		t.Errorf("the intro %s does not name the mode", body)
	}
}
