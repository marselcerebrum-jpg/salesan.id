package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/mediafetch"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// Quick replies: canned messages an operator drops into a chat by typing a
// slash and a shortcut.
//
// Everything here is scoped by application the same way custom variables are.
// The reason for repeating the pattern rather than generalising it is that the
// two have different lifetimes: a variable is rendered at send time by the
// campaign runner, a quick reply is text a person edits before sending. They
// look alike today and there is no reason to assume they will stay that way.

/**
 * scopedApplication resolves the application a piece of scoped content belongs
 * to, and refuses one the caller cannot reach.
 *
 * An empty or absent id means workspace-wide, which only a Leader may create:
 * a PIC writing a reply visible to brands they do not hold would be reaching
 * past their own remit through the back door.
 */
func scopedApplication(
	w http.ResponseWriter, sc repository.Scope, raw *string,
) (*uuid.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		if !sc.All {
			writeError(w, http.StatusForbidden, "forbidden",
				"Hanya Leader yang dapat membuat ini untuk semua aplikasi. Pilih satu aplikasi.")
			return nil, false
		}
		return nil, true
	}
	id, err := uuid.Parse(strings.TrimSpace(*raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_uuid", "ID aplikasi tidak valid")
		return nil, false
	}
	if !sc.CanSeeApplication(id) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Aplikasi ini di luar tanggung jawab Anda")
		return nil, false
	}
	return &id, true
}

// normaliseShortcut turns whatever somebody typed into a shortcut that can
// actually be summoned.
//
// Three rules, each earning its place:
//
//   - Lowercased, and the leading slash dropped. The slash belongs to the
//     interface that calls the reply, not to the record; pasting "/salam"
//     should do what it looks like it does.
//   - Every run of anything else becomes one underscore. Real sets of canned
//     replies are full of shortcuts like "FOLLOW UP" and "syarat tamtama",
//     and a space cannot work: the slash menu in the chat composer closes the
//     moment one is typed, because a sentence that happens to begin with a
//     slash is not somebody choosing a shortcut. Refusing those rows would
//     mean refusing most of a real file; renaming them keeps the words and
//     costs one new shortcut to remember.
//   - Trimmed of underscores at either end, and cut to forty characters, so
//     what comes out always satisfies the column's own rule.
//
// Returns "" when nothing usable is left, which the caller reports rather than
// saving under a name nobody chose.
func normaliseShortcut(raw string) string {
	lowered := strings.ToLower(strings.TrimSpace(raw))
	lowered = strings.TrimLeft(lowered, "/")

	var b strings.Builder
	lastWasSep := false
	for _, r := range lowered {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			lastWasSep = false
		default:
			if !lastWasSep && b.Len() > 0 {
				b.WriteByte('_')
				lastWasSep = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if len([]rune(out)) > 40 {
		out = string([]rune(out)[:40])
		out = strings.Trim(out, "_")
	}
	return out
}

func (s *Server) handleListQuickReplies(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	// The chat room asks for active rows only; the settings screen wants the
	// disabled ones too, so it can re-enable them.
	activeOnly := r.URL.Query().Get("active") == "true"

	replies, err := s.repo.ListQuickReplies(r.Context(), sc.WorkspaceID, sc.ApplicationIDs, sc.All, activeOnly)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quick_replies": replies})
}

func (s *Server) handleUpsertQuickReply(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengelola balas cepat")
		return
	}

	var req struct {
		// ID present means edit that row, which is what lets a shortcut or an
		// application be CHANGED. Absent means create or replace by shortcut.
		ID            *string `json:"id"`
		Shortcut      string  `json:"shortcut"`
		ApplicationID *string `json:"application_id"`
		Title         string  `json:"title"`
		Body          string  `json:"body"`
		Category      *string `json:"category"`
		// MediaURL turns this into a picture reply. Only the address is kept;
		// the bytes are fetched again at send time and discarded.
		MediaURL *string `json:"media_url"`
		IsActive *bool   `json:"is_active"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	appID, ok := scopedApplication(w, sc, req.ApplicationID)
	if !ok {
		return
	}

	// Repaired rather than refused: spaces become underscores, the slash is
	// dropped, and the case is lowered. Somebody typing "Follow Up" gets
	// /follow_up, which is a shortcut that works, instead of an error message
	// about characters.
	shortcut := normaliseShortcut(req.Shortcut)
	if shortcut == "" {
		writeError(w, http.StatusBadRequest, "invalid_shortcut",
			"Pintasan harus mengandung minimal satu huruf atau angka")
		return
	}

	body := strings.TrimSpace(req.Body)
	if body == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Isi balasan tidak boleh kosong")
		return
	}
	if len([]rune(body)) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Isi balasan terlalu panjang (maksimal 4096 karakter)")
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = shortcut
	}
	if len([]rune(title)) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_title", "Judul terlalu panjang")
		return
	}

	if req.Category != nil && len([]rune(strings.TrimSpace(*req.Category))) > 60 {
		writeError(w, http.StatusBadRequest, "invalid_category", "Kategori terlalu panjang")
		return
	}

	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}

	in := models.QuickReply{
		ApplicationID: appID,
		Shortcut:      shortcut,
		Title:         title,
		Body:          body,
		Category:      req.Category,
		IsActive:      active,
	}

	// The picture is validated by actually fetching it, now, at the moment the
	// address is saved.
	//
	// Not at send time: a quick reply is pressed mid-conversation with a
	// customer waiting, and "the link is dead" is not something to discover
	// there. And not from the extension either — what decides whether this is
	// an image is the bytes, which is why the fetch is the check.
	//
	// The same fence as Broadcast media: HTTPS only, no private or metadata
	// addresses, redirect limit, timeout, size limit. The bytes are thrown away
	// immediately; only the address and what was measured about it are stored.
	if req.MediaURL != nil && strings.TrimSpace(*req.MediaURL) != "" {
		res, err := mediafetch.Fetch(r.Context(), *req.MediaURL, mediaLimitsFor(s), false)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_media", mediaErrorText(err))
			return
		}
		defer res.Cleanup()

		if string(res.Info.Kind) != "image" {
			writeError(w, http.StatusBadRequest, "invalid_media",
				"Balas cepat hanya menerima gambar. Video dan dokumen tidak bisa dipakai di sini.")
			return
		}

		url := strings.TrimSpace(*req.MediaURL)
		kind := string(res.Info.Kind)
		mime := res.Info.MIME
		size := res.Info.Size
		sha := res.SHA256
		in.MediaURL = &url
		in.MediaKind = &kind
		in.MediaMime = &mime
		in.MediaSizeBytes = &size
		in.MediaSHA256 = &sha
	}

	var (
		q   *models.QuickReply
		err error
	)
	if req.ID != nil && strings.TrimSpace(*req.ID) != "" {
		id, perr := uuid.Parse(strings.TrimSpace(*req.ID))
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID balas cepat tidak valid")
			return
		}
		// The row's CURRENT owner decides who may edit it, checked before the
		// new one: without this, a PIC could take over another brand's reply by
		// pointing it at their own application.
		owner, oerr := s.repo.QuickReplyApplication(r.Context(), sc.WorkspaceID, id)
		if oerr != nil {
			writeAppError(w, oerr)
			return
		}
		if owner == nil && !sc.All {
			writeError(w, http.StatusForbidden, "forbidden",
				"Balas cepat untuk semua aplikasi hanya dapat diubah Leader")
			return
		}
		if owner != nil && !sc.CanSeeApplication(*owner) {
			writeError(w, http.StatusForbidden, "forbidden",
				"Aplikasi ini di luar tanggung jawab Anda")
			return
		}
		q, err = s.repo.UpdateQuickReply(r.Context(), sc.WorkspaceID, id, in)
	} else {
		q, err = s.repo.UpsertQuickReply(r.Context(), sc.WorkspaceID, user.ID, in)
	}
	if err != nil {
		if pg := repository.AsPgError(err); pg != nil && pg.Code == "23505" {
			writeError(w, http.StatusConflict, "shortcut_taken",
				"Pintasan /"+shortcut+" sudah dipakai balas cepat lain di aplikasi ini.")
			return
		}
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quick_reply": q})
}

// handleSendQuickReply sends one picture reply into a conversation.
//
// Only picture replies come through here. A text reply is pasted into the
// composer and edited before it goes, which is the whole point of a canned
// answer; an image cannot be pasted into a text box, so it is sent whole.
//
// The image is fetched HERE, server-side, from the address stored on the reply
// — the browser never sees the URL and never handles the bytes. That is what
// keeps the SSRF fence in one place: the same fetch, with the same limits, that
// validated the address when it was saved.
func (s *Server) handleSendQuickReply(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	conversationID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "conversation_id")
	if !ok {
		return
	}
	replyID, ok := parseUUIDParam(w, chi.URLParam(r, "quickReplyID"), "quick_reply_id")
	if !ok {
		return
	}

	// Read through the caller's own scope, so a reply belonging to an
	// application they do not hold is never reachable by guessing its id.
	replies, err := s.repo.ListQuickReplies(
		r.Context(), sc.WorkspaceID, sc.ApplicationIDs, sc.All, true)
	if err != nil {
		writeAppError(w, err)
		return
	}
	var reply *models.QuickReply
	for i := range replies {
		if replies[i].ID == replyID {
			reply = &replies[i]
			break
		}
	}
	if reply == nil {
		writeError(w, http.StatusNotFound, "not_found", "Balas cepat tidak ditemukan")
		return
	}
	if reply.MediaURL == nil {
		writeError(w, http.StatusBadRequest, "no_media",
			"Balas cepat ini hanya teks. Sisipkan ke kolom pesan, jangan dikirim lewat sini.")
		return
	}
	if !s.manager.MediaEnabled() {
		writeError(w, http.StatusServiceUnavailable, "media_disabled",
			"Penyimpanan media tidak siap. Periksa log backend.")
		return
	}

	// The caption the operator actually typed. Sent as a pointer so "" can mean
	// "no words, on purpose" — a picture with no caption is a real message —
	// while an absent field falls back to the reply's stored text.
	var req struct {
		ClientToken string  `json:"client_token"`
		Caption     *string `json:"caption"`
	}
	if err := decodeJSONOptional(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	caption := reply.Body
	if req.Caption != nil {
		caption = strings.TrimSpace(*req.Caption)
	}
	if len([]rune(caption)) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Caption terlalu panjang (maksimal 4096 karakter)")
		return
	}

	res, err := mediafetch.Fetch(r.Context(), *reply.MediaURL, mediaLimitsFor(s), false)
	if err != nil {
		writeError(w, http.StatusBadGateway, "invalid_media",
			"Gambar balas cepat ini tidak bisa diambil: "+mediaErrorText(err))
		return
	}
	defer res.Cleanup()

	if string(res.Info.Kind) != "image" {
		// The address still resolves, but to something else than it did when it
		// was saved. Refused rather than sent: whatever is at the other end now
		// is not what anybody approved.
		writeError(w, http.StatusBadGateway, "invalid_media",
			"Alamat gambar ini sekarang berisi berkas lain. Perbarui balas cepatnya.")
		return
	}

	msg, err := s.manager.SendMedia(r.Context(), wa.SendMediaInput{
		WorkspaceID:    user.WorkspaceID,
		ConversationID: conversationID,
		SentBy:         user.ID,
		File:           res.Info,
		Caption:        caption,
		ClientToken:    req.ClientToken,
		Source:         res.File,
	})
	if err != nil {
		if msg != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"message": msg, "error": "send_failed", "detail": err.Error(),
			})
			return
		}
		writeAppError(w, err)
		return
	}
	// Counted here rather than by the browser: this path both sends and knows
	// which reply it sent, so there is nothing to tell anybody.
	if uerr := s.repo.MarkQuickReplyUsed(r.Context(), sc.WorkspaceID, replyID); uerr != nil {
		s.log.Warn("mark quick reply used", "quick_reply_id", replyID, "err", uerr)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

// handleMarkQuickReplyUsed records that a text reply was actually sent.
//
// Called by the composer after the message has gone, not when the reply was
// picked: picking one and then clearing the box is not use. Picture replies do
// not come through here — the server sends those itself and counts them there.
//
// Answers 204 whatever happens. The message is already delivered; reporting a
// failure now would tell the operator their send failed when it did not.
func (s *Server) handleMarkQuickReplyUsed(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "quick_reply_id")
	if !ok {
		return
	}
	if err := s.repo.MarkQuickReplyUsed(r.Context(), sc.WorkspaceID, id); err != nil {
		s.log.Warn("mark quick reply used", "quick_reply_id", id, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteAllQuickReplies clears the caller's quick replies.
//
// Bounded twice over. The caller's own scope decides what is reachable at all —
// a PIC clearing their brand cannot touch another brand's replies, nor the
// workspace-wide ones, which belong to the Leader. And `application_id` narrows
// it further to whatever is on screen, so pressing this while looking at one
// application does what it looks like it does.
//
// Safe to offer because it is recoverable in practice: the export is a file of
// exactly these rows, and importing it back restores them. That is what the
// interface says before it asks.
func (s *Server) handleDeleteAllQuickReplies(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengelola balas cepat")
		return
	}

	var applicationID *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("application_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID aplikasi tidak valid")
			return
		}
		if !sc.CanSeeApplication(id) {
			writeError(w, http.StatusForbidden, "forbidden",
				"Aplikasi ini di luar tanggung jawab Anda")
			return
		}
		applicationID = &id
	}

	n, err := s.repo.DeleteAllQuickReplies(
		r.Context(), sc.WorkspaceID, sc.ApplicationIDs, sc.All, applicationID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (s *Server) handleDeleteQuickReply(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengelola balas cepat")
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "quick_reply_id")
	if !ok {
		return
	}

	// Read the owner before deleting: the RLS policy would refuse a row out of
	// reach, but a refusal that looks like "not found" tells a PIC nothing, and
	// the service role path does not apply RLS at all.
	appID, err := s.repo.QuickReplyApplication(r.Context(), sc.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if appID == nil {
		if !sc.All {
			writeError(w, http.StatusForbidden, "forbidden",
				"Balas cepat untuk semua aplikasi hanya dapat dihapus Leader")
			return
		}
	} else if !sc.CanSeeApplication(*appID) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Aplikasi ini di luar tanggung jawab Anda")
		return
	}

	if err := s.repo.DeleteQuickReply(r.Context(), sc.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
