package http

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/notifications/application/query"
	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
)

const eventField = "event"

// GetNotificationPreferences implements GET /me/notification-preferences:
// the caller's seven switches, a default for each one never saved, a
// student's email equal to the same switch in the app.
func (h Notifications) GetNotificationPreferences(ctx context.Context, _ openapi.GetNotificationPreferencesRequestObject) (openapi.GetNotificationPreferencesResponseObject, error) {
	userID, ok := h.caller(ctx)
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	prefs, err := h.app.Queries.Preferences.Handle(ctx, query.Preferences{UserID: userID, Recipient: recipientOf(ctx)})
	if err != nil {
		return nil, err
	}
	return openapi.GetNotificationPreferences200JSONResponse(toAPIPreferences(prefs)), nil
}

// UpdateNotificationPreferences implements PUT /me/notification-preferences.
// The contract's schema refuses an unknown event and any count but seven; a
// repeated event passes it and is refused here. A student's email is stored
// equal to the same switch in the app, whatever the body says (DG-82).
func (h Notifications) UpdateNotificationPreferences(ctx context.Context, request openapi.UpdateNotificationPreferencesRequestObject) (openapi.UpdateNotificationPreferencesResponseObject, error) {
	userID, ok := h.caller(ctx)
	if !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	given := make([]domain.Preference, len(*request.Body))
	for i, p := range *request.Body {
		given[i] = domain.Preference{Event: domain.Event(p.Event), InApp: p.InApp, Email: p.Email}
	}
	stored, err := h.app.Commands.UpdatePreferences.Handle(ctx, command.UpdatePreferences{UserID: userID, Recipient: recipientOf(ctx), Preferences: given})
	switch {
	case errors.Is(err, domain.ErrUnknownEvent):
		return openapi.UpdateNotificationPreferences400JSONResponse(
			httpapi.FieldError(ctx, eventField, httpx.Text(ctx, "Loại thông báo không hợp lệ.", "The notification type is not valid."))), nil
	case errors.Is(err, domain.ErrEventsNotOnceEach):
		return openapi.UpdateNotificationPreferences400JSONResponse(
			httpapi.FieldError(ctx, eventField, httpx.Text(ctx, "Mỗi loại thông báo phải xuất hiện đúng một lần.", "Each notification type must appear exactly once."))), nil
	case err != nil:
		return nil, err
	}
	return openapi.UpdateNotificationPreferences200JSONResponse(toAPIPreferences(stored)), nil
}

func toAPIPreferences(prefs []domain.Preference) []openapi.NotificationPreference {
	out := make([]openapi.NotificationPreference, len(prefs))
	for i, p := range prefs {
		out[i] = openapi.NotificationPreference{Event: openapi.NotificationEvent(p.Event), InApp: p.InApp, Email: p.Email}
	}
	return out
}

// recipientOf is whose switches the caller's are: a student's when the
// caller's role passes R2's strict student predicate, a staff member's
// otherwise.
func recipientOf(ctx context.Context) domain.Recipient {
	principal, _ := httpx.PrincipalFromContext(ctx)
	if access.IsStudentLike(principal.Access.BuiltinKey, principal.Access.Permissions) {
		return domain.Student
	}
	return domain.Staff
}
