package repository

import (
	"context"

	"github.com/google/uuid"
)

// Guards for objects addressed directly by id.
//
// The listing queries all narrow by the caller's applications, but a listing is
// not the only way into a row: an id in a URL reaches the same data without
// passing through any of them. Until these existed, a Freelance who knew a
// conversation id could open any thread in the workspace — the interface would
// not show it to them, and the API would hand it over anyway.
//
// Hiding a row in the frontend is not access control. These are the checks that
// actually refuse.

// ApplicationOfAccount returns the application a WhatsApp number belongs to.
// Null when the number has not been assigned to one yet.
func (r *Repo) ApplicationOfAccount(ctx context.Context, workspaceID, accountID uuid.UUID) (*uuid.UUID, error) {
	var app *uuid.UUID
	err := r.pool.QueryRow(ctx,
		`select application_id from public.whatsapp_accounts
		  where id = $1 and workspace_id = $2`, accountID, workspaceID).Scan(&app)
	if err != nil {
		return nil, mapErr(err)
	}
	return app, nil
}

// AccountInScope reports whether the caller may act on one WhatsApp number.
//
// An unassigned number is visible only to a Leader. That is deliberate: a
// number with no application sits outside the hierarchy entirely, and the
// person who resolves that is the one who assigns applications.
func (r *Repo) AccountInScope(ctx context.Context, sc Scope, accountID uuid.UUID) (bool, error) {
	app, err := r.ApplicationOfAccount(ctx, sc.WorkspaceID, accountID)
	if err != nil {
		return false, err
	}
	if sc.All {
		return true, nil
	}
	if app == nil {
		return false, nil
	}
	return sc.CanSeeApplication(*app), nil
}

// ConversationInScope reports whether the caller may open one thread.
//
// Resolved through the conversation's account to its application, which is the
// only chain that exists: a conversation belongs to a number, and a number
// belongs to an application. There is no per-conversation assignment, and
// inventing one here would create a second authorization model beside the one
// the rest of the system uses.
func (r *Repo) ConversationInScope(ctx context.Context, sc Scope, conversationID uuid.UUID) (bool, error) {
	var app *uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select a.application_id
		  from public.conversations c
		  join public.whatsapp_accounts a on a.id = c.account_id
		 where c.id = $1 and c.workspace_id = $2`,
		conversationID, sc.WorkspaceID).Scan(&app)
	if err != nil {
		return false, mapErr(err)
	}
	if sc.All {
		return true, nil
	}
	if app == nil {
		return false, nil
	}
	return sc.CanSeeApplication(*app), nil
}

// ConversationLocation is what a deep link needs to open the right room.
//
// The route is /chat/{applicationId}/{accountId}, and the thread is chosen
// inside it — so a link built from a phone number alone lands in the wrong
// place whenever the same customer has written to two of our numbers. All three
// ids travel together for that reason.
type ConversationLocation struct {
	ConversationID uuid.UUID  `json:"conversation_id"`
	AccountID      uuid.UUID  `json:"account_id"`
	ApplicationID  *uuid.UUID `json:"application_id"`
}

// LocateConversation resolves the ids behind one thread, refusing anything
// outside the caller's scope.
func (r *Repo) LocateConversation(
	ctx context.Context, sc Scope, conversationID uuid.UUID,
) (*ConversationLocation, error) {
	var loc ConversationLocation
	err := r.pool.QueryRow(ctx, `
		select c.id, c.account_id, a.application_id
		  from public.conversations c
		  join public.whatsapp_accounts a on a.id = c.account_id
		 where c.id = $1 and c.workspace_id = $2`,
		conversationID, sc.WorkspaceID).Scan(&loc.ConversationID, &loc.AccountID, &loc.ApplicationID)
	if err != nil {
		return nil, mapErr(err)
	}
	if !sc.All && (loc.ApplicationID == nil || !sc.CanSeeApplication(*loc.ApplicationID)) {
		return nil, ErrForbidden
	}
	return &loc, nil
}

// CampaignInScope reports whether the caller may see one campaign.
//
// Same rule the RLS policy states, expressed for the API path where the service
// role means RLS never runs: the campaign's application must be visible, or the
// caller must have created it.
func (r *Repo) CampaignInScope(ctx context.Context, sc Scope, campaignID uuid.UUID) (bool, error) {
	var app *uuid.UUID
	var creator *uuid.UUID
	err := r.pool.QueryRow(ctx,
		`select application_id, created_by from public.content_campaigns
		  where id = $1 and workspace_id = $2`, campaignID, sc.WorkspaceID).Scan(&app, &creator)
	if err != nil {
		return false, mapErr(err)
	}
	if sc.All {
		return true, nil
	}
	if creator != nil && *creator == sc.UserID {
		return true, nil
	}
	return app != nil && sc.CanSeeApplication(*app), nil
}
