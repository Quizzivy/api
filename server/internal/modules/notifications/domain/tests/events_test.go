package domain_test

import (
	"errors"
	"slices"
	"testing"

	"quizzivy/internal/modules/notifications/domain"
)

// theSeven are a staff member's defaults, as the deck draws them.
func theSeven() []domain.Preference {
	return []domain.Preference{
		{Event: domain.EventAttemptSubmitted, InApp: true},
		{Event: domain.EventAttemptFlagged, InApp: true, Email: true},
		{Event: domain.EventAssignmentClosing, InApp: true},
		{Event: domain.EventAssignmentDueSoon, InApp: true, Email: true},
		{Event: domain.EventResultReady, InApp: true, Email: true},
		{Event: domain.EventMessageReceived, InApp: true, Email: true},
		{Event: domain.EventSummaryWeekly, InApp: false, Email: true},
	}
}

func TestTheSwitchesAreTheContractsEventsInItsOrder(t *testing.T) {
	events := schema(t, "NotificationEvent")
	declared := make([]domain.Event, 0, len(events.Enum))
	for _, v := range events.Enum {
		declared = append(declared, domain.Event(v.(string)))
	}
	if !slices.Equal(domain.Events(), declared) {
		t.Errorf("the switches are %v, the contract's events %v", domain.Events(), declared)
	}
	set := schema(t, "NotificationPreferences")
	if set.MaxItems == nil {
		t.Fatal("the contract puts no cap on the preferences")
	}
	if set.MinItems != uint64(len(declared)) || *set.MaxItems != uint64(len(declared)) {
		t.Errorf("the contract takes %d to %d preferences, want exactly %d", set.MinItems, *set.MaxItems, len(declared))
	}
	domain.Events()[0] = "changed"
	if domain.Events()[0] != domain.EventAttemptSubmitted {
		t.Error("a caller can change the list of switches")
	}
}

// TestASwitchNeverSavedIsTheDecksDefault fails when a default drifts from
// the deck's table (NotificationEvent): a teacher gets a flag, a message and
// the weekly summary by email and not a submission or a closing, the weekly
// summary is off in the app, and a student's email always follows the app.
func TestASwitchNeverSavedIsTheDecksDefault(t *testing.T) {
	if got := domain.WithDefaults(nil, domain.Staff); !slices.Equal(got, theSeven()) {
		t.Errorf("a teacher's defaults are %+v, want %+v", got, theSeven())
	}
	for _, p := range domain.WithDefaults(nil, domain.Student) {
		want := p.Event != domain.EventSummaryWeekly
		if p.InApp != want || p.Email != want {
			t.Errorf("a student's default for %s is %+v, want both channels %v", p.Event, p, want)
		}
	}
	for _, event := range domain.Events() {
		if got := domain.DefaultPreference(event, domain.Staff); got.InApp != domain.DefaultInApp(event) {
			t.Errorf("the default for %s is %+v, DefaultInApp says %v", event, got, domain.DefaultInApp(event))
		}
	}
}

func TestASavedSwitchWinsOverItsDefault(t *testing.T) {
	stored := []domain.Preference{
		{Event: domain.EventResultReady, InApp: false, Email: true},
		{Event: domain.EventAttemptFlagged, InApp: true, Email: false},
		{Event: "class.joined", InApp: true, Email: true},
	}
	want := theSeven()
	want[1].Email = false
	want[4] = domain.Preference{Event: domain.EventResultReady, InApp: false, Email: true}
	if got := domain.WithDefaults(stored, domain.Staff); !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestAStudentsEmailIsTheirInAppSwitch fails when a student's stored or given
// email can differ from the same switch in the app (DG-82): a row saved before
// R7, with email false, still reads and sends as the app switch says.
func TestAStudentsEmailIsTheirInAppSwitch(t *testing.T) {
	stored := []domain.Preference{
		{Event: domain.EventResultReady, InApp: true, Email: false},
		{Event: domain.EventAssignmentDueSoon, InApp: false, Email: true},
	}
	for _, p := range domain.WithDefaults(stored, domain.Student) {
		if p.Email != p.InApp {
			t.Errorf("a student's %s reads %+v", p.Event, p)
		}
	}
	kept := domain.Student.Keep(stored)
	if kept[0].Email != true || kept[1].Email != false || stored[0].Email {
		t.Errorf("Keep gives %+v and must not change what it was given (%+v)", kept, stored)
	}
	if got := domain.Staff.Keep(stored); !slices.Equal(got, stored) {
		t.Errorf("a staff member's switches changed: %+v", got)
	}
}

func TestAWholeSetHoldsEverySwitchExactlyOnce(t *testing.T) {
	given := theSeven()
	given[0].InApp, given[3].Email = false, true
	want := slices.Clone(given)
	slices.Reverse(given)
	got, err := domain.WholeSet(given)
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("a reversed set gives %+v (%v), want it in the switches' order: %+v", got, err, want)
	}

	repeated := theSeven()
	repeated[4] = domain.Preference{Event: domain.EventAttemptSubmitted, InApp: false}
	if _, err := domain.WholeSet(repeated); !errors.Is(err, domain.ErrEventsNotOnceEach) {
		t.Errorf("a repeated event: err = %v, want ErrEventsNotOnceEach", err)
	}
	if _, err := domain.WholeSet(append(theSeven(), theSeven()[2])); !errors.Is(err, domain.ErrEventsNotOnceEach) {
		t.Errorf("an eighth switch: err = %v, want ErrEventsNotOnceEach", err)
	}
	if _, err := domain.WholeSet(theSeven()[:4]); !errors.Is(err, domain.ErrEventsNotOnceEach) {
		t.Errorf("a missing event: err = %v, want ErrEventsNotOnceEach", err)
	}
	if _, err := domain.WholeSet(nil); !errors.Is(err, domain.ErrEventsNotOnceEach) {
		t.Errorf("no switch at all: err = %v, want ErrEventsNotOnceEach", err)
	}
	unknown := theSeven()
	unknown[2].Event = "class.joined"
	if _, err := domain.WholeSet(unknown); !errors.Is(err, domain.ErrUnknownEvent) {
		t.Errorf("an event that is not a switch: err = %v, want ErrUnknownEvent", err)
	}
}

func TestAPageIsSizedWithinTheContractsLimits(t *testing.T) {
	var limit *struct{ def, max, min float64 }
	for _, p := range contract(t).Paths.Find("/me/notifications").Get.Parameters {
		if p.Value.Name == "limit" {
			s := p.Value.Schema.Value
			limit = &struct{ def, max, min float64 }{s.Default.(float64), *s.Max, *s.Min}
		}
	}
	if limit == nil {
		t.Fatal("listNotifications declares no limit")
	}
	if int(limit.def) != domain.DefaultLimit || int(limit.max) != domain.MaxLimit || limit.min != 1 {
		t.Errorf("the contract's limit is %+v, the domain's default %d and maximum %d", *limit, domain.DefaultLimit, domain.MaxLimit)
	}
	for given, want := range map[int]int{
		-1: domain.DefaultLimit, 0: domain.DefaultLimit, 1: 1, 20: 20,
		domain.MaxLimit: domain.MaxLimit, domain.MaxLimit + 1: domain.MaxLimit, 100000: domain.MaxLimit,
	} {
		if got := (domain.ListQuery{Limit: given}).Size(); got != want {
			t.Errorf("a limit of %d gives a page of %d, want %d", given, got, want)
		}
	}
	if domain.DefaultLimit != 20 || domain.MaxLimit != 50 {
		t.Errorf("the page is %d by default and %d at most, want 20 and 50", domain.DefaultLimit, domain.MaxLimit)
	}
}

func TestTheCapOnMarkedIdsIsTheContracts(t *testing.T) {
	body := contract(t).Paths.Find("/me/notifications/read").Post.RequestBody.Value.Content.Get("application/json").Schema.Value
	ids := body.Properties["ids"].Value
	if ids.MaxItems == nil {
		t.Fatal("the contract puts no cap on ids")
	}
	if int(*ids.MaxItems) != domain.MaxMarkedIDs || ids.MinItems != 1 {
		t.Errorf("the contract takes %d to %d ids, the domain 1 to %d", ids.MinItems, *ids.MaxItems, domain.MaxMarkedIDs)
	}
	if domain.MaxMarkedIDs != 100 {
		t.Errorf("the cap is %d, want 100", domain.MaxMarkedIDs)
	}
	if slices.Contains(body.Required, "ids") {
		t.Error("the contract requires ids, so nothing can ask for every unread notification")
	}
}
