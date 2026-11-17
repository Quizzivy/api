package router_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// propertyNamesOf collects every property name reachable from ref, through
// items, allOf, oneOf, anyOf and additionalProperties, and the names of the
// component schemas it passes through.
func propertyNamesOf(spec *openapi3.T, ref *openapi3.SchemaRef, names, schemas map[string]bool, seen map[*openapi3.Schema]bool) {
	if ref == nil || ref.Value == nil || seen[ref.Value] {
		return
	}
	if ref.Ref != "" {
		schemas[strings.TrimPrefix(ref.Ref, "#/components/schemas/")] = true
	}
	s := ref.Value
	seen[s] = true
	for name, prop := range s.Properties {
		names[name] = true
		propertyNamesOf(spec, prop, names, schemas, seen)
	}
	propertyNamesOf(spec, s.Items, names, schemas, seen)
	propertyNamesOf(spec, s.AdditionalProperties.Schema, names, schemas, seen)
	for _, group := range []openapi3.SchemaRefs{s.AllOf, s.OneOf, s.AnyOf} {
		for _, sub := range group {
			propertyNamesOf(spec, sub, names, schemas, seen)
		}
	}
}

// successNames walks the JSON bodies of an operation's 2xx answers.
func successNames(spec *openapi3.T, op *openapi3.Operation) (names, schemas map[string]bool) {
	names, schemas = map[string]bool{}, map[string]bool{}
	for status, response := range op.Responses.Map() {
		if !strings.HasPrefix(status, "2") || response.Value == nil {
			continue
		}
		if media := response.Value.Content.Get("application/json"); media != nil {
			propertyNamesOf(spec, media.Schema, names, schemas, map[*openapi3.Schema]bool{})
		}
	}
	return names, schemas
}

// The keys no answer under /app/messages may carry (D17, DG-91, §13.5): the
// grading keys every /app/* answer is walked for, the staff side's "Seen by"
// and attempt events, any count of a class's members, and an email.
var studentMessagingBanned = []string{
	"isCorrect", "sampleAnswer", "acceptedAnswers", "transcript", "transcripts", "teacherNote",
	"seenBy", "contextEvents", "studentCount", "memberCount", "email", "side",
}

func TestTheStudentMessagesAnswerNoStaffKey(t *testing.T) {
	spec := freshSpec(t)
	walked := 0
	reached := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		if !strings.HasPrefix(path, "/app/messages/") {
			continue
		}
		for method, op := range item.Operations() {
			names, schemas := successNames(spec, op)
			walked++
			maps.Copy(reached, names)
			for _, bad := range studentMessagingBanned {
				if names[bad] {
					t.Errorf("%s %s answers %q", method, path, bad)
				}
			}
			for _, staff := range []string{"StaffMessage", "StaffMessageSender", "SeenBy", "ContextEvent", "StaffThread", "StaffDirectThreadPeer", "StaffClassThreadClass"} {
				if schemas[staff] {
					t.Errorf("%s %s answers the staff schema %s", method, path, staff)
				}
			}
		}
	}
	if walked < 7 {
		t.Fatalf("walked %d operations under /app/messages, want the seven of the contract", walked)
	}
	for _, key := range []string{"threads", "messages", "sender", "teacher", "readOnly", "classNames", "former", "canPost", "preview", "unread", "mine"} {
		if !reached[key] {
			t.Errorf("the walk never reaches %q, so it is not reading the answers it thinks it is", key)
		}
	}
}

func TestTheStaffAndStudentMessagesAreDistinctSchemas(t *testing.T) {
	spec := freshSpec(t)
	schemas := spec.Components.Schemas
	staff, student := schemas["StaffMessage"], schemas["StudentMessage"]
	if staff == nil || student == nil {
		t.Fatal("the contract has no StaffMessage or StudentMessage")
	}
	if staff.Value == student.Value {
		t.Fatal("StaffMessage and StudentMessage are one schema")
	}
	keys := func(s *openapi3.SchemaRef) []string { return slices.Sorted(maps.Keys(s.Value.Properties)) }
	if slices.Contains(keys(student), "seenBy") || !slices.Contains(keys(staff), "seenBy") {
		t.Errorf("StaffMessage has %v and StudentMessage %v: only the staff one carries seenBy", keys(staff), keys(student))
	}
	if student.Value.Properties["sender"].Ref == staff.Value.Properties["sender"].Ref {
		t.Errorf("both messages name their sender with %s: the student's sender carries no side", student.Value.Properties["sender"].Ref)
	}
	for _, op := range []string{"getStaffDirectThread", "postStaffDirectMessage"} {
		var found *openapi3.Operation
		eachOperation(spec, func(_ string, o *openapi3.Operation) {
			if strings.EqualFold(o.OperationID, op) {
				found = o
			}
		})
		if found == nil {
			t.Fatalf("the contract has no %s", op)
		}
		if names, _ := successNames(spec, found); !names["side"] || (op == "getStaffDirectThread" && !names["contextEvents"]) {
			t.Errorf("%s answers %v: the walk finds no staff key where there is one", op, slices.Sorted(maps.Keys(names)))
		}
	}
}
