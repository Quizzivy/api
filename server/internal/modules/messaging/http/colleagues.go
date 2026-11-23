package http

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/actor"
)

// The colleague threads (F2, T-F2.4) live under /teacher/messages/colleagues:
// a thread between two staff members, addressed on either side by the other
// member, readable by the two alone, scope.all included (D17). No attempt
// cards and no "Seen by"; nothing is audited.

func colleagueNotAllowed(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.MESSAGINGNOTALLOWED,
		httpx.Text(ctx, "Không thể nhắn cho đồng nghiệp này: tài khoản đã bị khoá hoặc không còn quyền vào không gian giáo viên.",
			"You cannot message this colleague: the account is disabled or no longer opens the teacher workspace."))
}

func builtinOf(key *string) *openapi.BuiltinRoleKey {
	if key == nil {
		return nil
	}
	return new(openapi.BuiltinRoleKey(*key))
}

func colleagueThreadOf(t domain.ColleagueThread) openapi.ColleagueThread {
	out := openapi.ColleagueThread{
		ColleagueId: httpapi.ParseUUID(t.ColleagueID), Title: t.Title, AvatarUrl: t.AvatarURL,
		RoleName: t.RoleName, RoleBuiltinKey: builtinOf(t.RoleBuiltinKey),
		Candidate: t.Candidate, Disabled: t.Disabled, Unread: t.Unread,
	}
	if t.LastMessageAt != nil {
		out.LastMessageAt = new(t.LastMessageAt.UTC())
	}
	if p := t.Preview; p != nil {
		out.Preview = &openapi.MessagePreview{Text: p.Text, AttachmentName: p.AttachmentName, Mine: p.Mine}
	}
	return out
}

// ListColleagueThreads implements GET /teacher/messages/colleagues.
func (h Staff) ListColleagueThreads(ctx context.Context, request openapi.ListColleagueThreadsRequestObject) (openapi.ListColleagueThreadsResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	q := query.ColleagueThreads{ActorID: who.ID}
	if request.Params.Q != nil {
		q.Query = string(*request.Params.Q)
	}
	threads, err := h.app.Queries.ColleagueThreads.Handle(ctx, q)
	if err != nil {
		return nil, err
	}
	out := openapi.ListColleagueThreads200JSONResponse{Threads: make([]openapi.ColleagueThread, len(threads))}
	for i, t := range threads {
		out.Threads[i] = colleagueThreadOf(t)
	}
	return out, nil
}

// GetColleagueThread implements GET /teacher/messages/colleagues/{colleagueId}.
func (h Staff) GetColleagueThread(ctx context.Context, request openapi.GetColleagueThreadRequestObject) (openapi.GetColleagueThreadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	p := request.Params
	thread, err := h.app.Queries.ColleagueThread.Handle(ctx, query.ColleagueThread{
		ActorID: who.ID, ColleagueID: request.ColleagueId.String(), Cursor: cursorOf(p.Before, p.After, p.Limit),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetColleagueThread404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.GetColleagueThread400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	peer := thread.Peer
	return openapi.GetColleagueThread200JSONResponse{
		Colleague: openapi.ColleagueDirectThreadPeer{
			Id: httpapi.ParseUUID(peer.ID), Name: peer.Name, AvatarUrl: peer.AvatarURL,
			RoleName: peer.RoleName, RoleBuiltinKey: builtinOf(peer.RoleBuiltinKey), Disabled: peer.Disabled, CanPost: peer.CanPost,
		},
		Messages:   messagesOf(thread.Page),
		NextBefore: uuidOf(thread.Page.NextBefore),
		LatestId:   uuidOf(thread.Page.LatestID),
	}, nil
}

// PostColleagueMessage implements POST /teacher/messages/colleagues/{colleagueId}.
func (h Staff) PostColleagueMessage(ctx context.Context, request openapi.PostColleagueMessageRequestObject) (openapi.PostColleagueMessageResponseObject, error) {
	posted, err := h.post(ctx, request.Body, func(who actor.Actor, in openapi.StaffMessagePost) (domain.Posted, error) {
		return h.app.Commands.PostColleague.Handle(ctx, command.PostColleague{
			Actor: who, ColleagueID: request.ColleagueId.String(), ClientID: in.ClientId.String(), Body: in.Body, AttachmentID: stringOf(in.AttachmentId),
		})
	})
	return answerPost[openapi.PostColleagueMessageResponseObject, openapi.PostColleagueMessage201JSONResponse, openapi.PostColleagueMessage200JSONResponse, openapi.PostColleagueMessage404JSONResponse,
		openapi.PostColleagueMessage409JSONResponse, openapi.PostColleagueMessage422JSONResponse, openapi.PostColleagueMessage400JSONResponse](ctx, posted, err, colleagueNotAllowed(ctx))
}

// MarkColleagueThreadRead implements POST /teacher/messages/colleagues/{colleagueId}/read.
func (h Staff) MarkColleagueThreadRead(ctx context.Context, request openapi.MarkColleagueThreadReadRequestObject) (openapi.MarkColleagueThreadReadResponseObject, error) {
	if h.app == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	err := h.markRead(ctx, domain.ThreadRef{Kind: domain.KindColleague, ColleagueID: request.ColleagueId.String()}, request.Body.LastReadMessageId)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.MarkColleagueThreadRead404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrNotInThread):
		return openapi.MarkColleagueThreadRead422JSONResponse(notInThread(ctx)), nil
	case err != nil:
		return nil, err
	}
	return openapi.MarkColleagueThreadRead204Response{}, nil
}
