package wa

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/types"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Why a number written inside a message cannot simply become a chat.
var (
	// ErrNumberUnreadable means the digits could not be read as a number at
	// all. Refused rather than guessed: the alternative is a thread addressed
	// to somebody who was never mentioned.
	ErrNumberUnreadable = errors.New("wa: nomor tidak dapat dibaca")

	// ErrNumberNotOnWhatsApp means WhatsApp itself says nobody is registered
	// there. Said out loud rather than opening an empty thread that would
	// silently fail on the first send.
	ErrNumberNotOnWhatsApp = errors.New("wa: nomor tidak terdaftar di whatsapp")
)

// NumberTarget is where a chat with a bare number lands.
//
// The same shape the private reply resolver returns, for the same reason: the
// browser needs to know which thread to open, and on a workspace with several
// numbers it also needs to know which of them it now belongs to.
type NumberTarget struct {
	ConversationID uuid.UUID  `json:"conversation_id"`
	AccountID      uuid.UUID  `json:"account_id"`
	ApplicationID  *uuid.UUID `json:"application_id"`
	Name           string     `json:"name"`
	PhoneNumber    string     `json:"phone_number"`
	// Whether the thread was already there.
	//
	// Worth saying: "membuka percakapan yang sudah ada" and "memulai
	// percakapan baru" are different actions, and an operator who cannot tell
	// them apart does not know whether they are about to write to a stranger.
	Existing bool `json:"existing"`
}

// ResolveNumberChat finds, or opens, the one-to-one thread for a number.
//
// The number arrives as it was typed inside somebody's message — "0812-3456-
// 7890", "+62 812 3456 7890", "62812…" — so it goes through the same
// NormalizePhone the contact import uses. One rule for reading a number, in
// one place: a second rule here would eventually disagree with that one, and
// the disagreement would show up as two threads for one person.
//
// Registration is checked against WhatsApp rather than assumed. A number
// scraped out of a sentence is frequently not on WhatsApp at all — a landline,
// a typo, an office extension — and opening a thread for it would leave a row
// in the inbox that fails on its first send with nothing explaining why.
//
// The thread is created here, before a word is typed, which is what makes it
// openable at all: our model has no concept of an unsaved chat. The operator
// can delete it again like any other.
func (m *Manager) ResolveNumberChat(
	ctx context.Context,
	workspaceID, accountID uuid.UUID,
	raw string,
) (*NumberTarget, error) {
	phone := repository.NormalizePhone(raw)
	if phone == "" {
		return nil, ErrNumberUnreadable
	}

	appID, err := m.repo.ApplicationOfAccount(ctx, workspaceID, accountID)
	if err != nil {
		return nil, err
	}

	pnJID := types.NewJID(phone, types.DefaultUserServer)

	// Already ours: answered without asking WhatsApp anything. The lookup
	// matches on pn_jid as well as chat_jid, so it finds the thread even when
	// that thread is keyed by the LID this number hides behind.
	if existing, err := m.repo.GetConversationByAccountChat(ctx, accountID, pnJID.String()); err == nil {
		return &NumberTarget{
			ConversationID: existing.ID,
			AccountID:      accountID,
			ApplicationID:  appID,
			Name:           textOr(existing.Name, phone),
			PhoneNumber:    phone,
			Existing:       true,
		}, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	s, ok := m.Session(accountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}

	found, err := s.client.IsOnWhatsApp(ctx, []string{phone})
	if err != nil {
		return nil, fmt.Errorf("periksa nomor di whatsapp: %w", err)
	}
	if len(found) == 0 || !found[0].IsIn {
		return nil, ErrNumberNotOnWhatsApp
	}

	// WhatsApp's own answer about who this is, which is the authority on both
	// counts: the canonical address to key the thread by, and the number form
	// that lets a later message addressed the other way find it.
	chatJID := found[0].JID.ToNonAD()
	pn := found[0].PhoneNumber.ToNonAD()
	if pn.IsEmpty() {
		pn = pnJID
	}

	// Asked again, now that WhatsApp has named the canonical address.
	//
	// The lookup above could only see threads reachable by number. A thread
	// keyed by this person's LID with no number recorded against it is not one
	// of those, and it is a thread the operator may well have been reading for
	// weeks under a name. Calling that "percakapan baru" would be a lie, so
	// the question is asked a second time rather than assumed.
	existing := false
	if _, err := m.repo.GetConversationByAccountChat(ctx, accountID, chatJID.String()); err == nil {
		existing = true
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	// A verified business name is the only name we have at this point. Anything
	// else would be invented: nobody has written to this number yet, so there
	// is no push name and no contact row.
	name := ""
	if vn := found[0].VerifiedName; vn != nil && vn.Details != nil {
		name = vn.Details.GetVerifiedName()
	}

	id, err := m.repo.UpsertConversation(ctx, repository.UpsertConversationInput{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		ChatJID:     chatJID.String(),
		PNJID:       pn.String(),
		Type:        models.ConversationTypePersonal,
		Name:        name,
	})
	if err != nil {
		return nil, err
	}

	conv, err := m.repo.GetConversationByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return &NumberTarget{
		ConversationID: conv.ID,
		AccountID:      accountID,
		ApplicationID:  appID,
		Name:           textOr(conv.Name, phone),
		PhoneNumber:    pn.User,
		Existing:       existing,
	}, nil
}
