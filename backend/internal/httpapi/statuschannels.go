package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// Status and Saluran, the two panels beside Chat.

// handleListStatus returns every live Status one number can see.
//
// Grouped in the browser rather than here: "mine" and "seen" are questions about
// who is reading, and the same list answers both without a second round trip.
func (s *Server) handleListStatus(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, accountID); err != nil {
		writeAppError(w, err)
		return
	}

	posts, err := s.repo.ListStatusPosts(r.Context(), user.WorkspaceID, accountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": posts})
}

// handleMarkStatusSeen records locally that the operator opened one Status.
//
// Local only. Nothing is sent to WhatsApp: a read receipt tells somebody their
// Status was watched, and manufacturing one because a list scrolled past would
// be exactly the kind of fake behaviour this product refuses elsewhere.
func (s *Server) handleMarkStatusSeen(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "messageID"), "message_id")
	if !ok {
		return
	}
	if err := s.repo.MarkStatusSeen(r.Context(), user.WorkspaceID, messageID); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seen": true})
}

// handleRevokeStatus takes one of our own Status posts off WhatsApp.
//
// Resolved through the workspace, then through the number's scope: a status
// belongs to one account, and a PIC reaching into a number outside their
// applications is refused here the same way as on every /accounts/{id} route.
func (s *Server) handleRevokeStatus(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	messageID, ok := parseUUIDParam(w, chi.URLParam(r, "messageID"), "message_id")
	if !ok {
		return
	}
	target, err := s.repo.MessageTargetByID(r.Context(), user.WorkspaceID, messageID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	account, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, target.AccountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !sc.All && (account.ApplicationID == nil || !sc.CanSeeApplication(*account.ApplicationID)) {
		writeError(w, http.StatusForbidden, "forbidden", "Nomor ini di luar wewenang akun Anda")
		return
	}

	if err := s.manager.RevokeOwnStatus(r.Context(), user.WorkspaceID, messageID); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// --- channels ----------------------------------------------------------------

func (s *Server) handleListNewsletters(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, accountID); err != nil {
		writeAppError(w, err)
		return
	}

	list, err := s.repo.ListNewsletters(r.Context(), user.WorkspaceID, accountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"newsletters": list})
}

// handleSyncNewsletters refreshes the directory from WhatsApp.
func (s *Server) handleSyncNewsletters(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, accountID); err != nil {
		writeAppError(w, err)
		return
	}

	n, err := s.manager.SyncNewsletters(r.Context(), accountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	list, err := s.repo.ListNewsletters(r.Context(), user.WorkspaceID, accountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"newsletters": list, "synced": n})
}

// handleNewsletterPosts reads a channel's recent posts straight from WhatsApp.
//
// Not stored, and not cached. They are somebody else's publication; a copy in
// our database would be storage and a duty of care bought for nothing.
func (s *Server) handleNewsletterPosts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	jid := strings.TrimSpace(r.URL.Query().Get("jid"))
	if jid == "" {
		writeError(w, http.StatusBadRequest, "missing_channel", "Saluran tidak disebutkan")
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, accountID); err != nil {
		writeAppError(w, err)
		return
	}

	posts, err := s.manager.NewsletterPosts(r.Context(), accountID, jid, queryInt(r, "limit", 30))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}

// handleNewsletterMedia streams the file behind one channel post.
//
// Requested with the caller's token by the browser and turned into a blob URL
// there, so there is no link to leak and nothing to expire: the bytes are
// fetched from WhatsApp, passed through, and forgotten. The channel must be one
// this workspace's number follows — checked against our own directory first.
func (s *Server) handleNewsletterMedia(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	jid := strings.TrimSpace(r.URL.Query().Get("jid"))
	serverID := strings.TrimSpace(r.URL.Query().Get("server_id"))
	if jid == "" || serverID == "" {
		writeError(w, http.StatusBadRequest, "missing_post", "Saluran atau postingan tidak disebutkan")
		return
	}
	if _, err := s.repo.GetNewsletter(r.Context(), user.WorkspaceID, accountID, jid); err != nil {
		writeAppError(w, err)
		return
	}

	file, err := s.manager.NewsletterMedia(r.Context(), accountID, jid, serverID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// The type comes from the bytes, not from what the post claimed: it decides
	// how the browser treats the response, so it must not be steerable. Anything
	// that would render as a page is sent as a download instead.
	mimeType := http.DetectContentType(file.Data)
	if strings.HasPrefix(mimeType, "text/html") || strings.Contains(mimeType, "svg") {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(file.Data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

// handlePostNewsletter publishes a text post or a poll to a channel we run.
//
// Text and polls here, files in handlePostNewsletterMedia, the same split the
// chat routes use: one is a small JSON body, the other is a streamed upload, and
// a single endpoint that did both would have to parse a multipart request to
// find out it was sending a sentence.
//
// The role check that actually decides is in the manager, against WhatsApp's own
// answer. The stored row is consulted first only to fail fast and to keep the
// channel inside this workspace — a JID from another tenant's account is not
// found here and never reaches the session.
func (s *Server) handlePostNewsletter(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}

	var req struct {
		JID  string `json:"jid"`
		Kind string `json:"kind"`
		// Text is the message for a text post, and the question for a poll.
		Text            string   `json:"text"`
		Options         []string `json:"options"`
		SelectableCount int      `json:"selectable_count"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	jid := strings.TrimSpace(req.JID)
	if jid == "" {
		writeError(w, http.StatusBadRequest, "missing_channel", "Saluran tidak disebutkan")
		return
	}
	if req.Kind != wa.PostText && req.Kind != wa.PostPoll {
		writeError(w, http.StatusBadRequest, "invalid_post",
			"Jenis postingan di sini hanya teks atau polling. Kirim berkas lewat endpoint media.")
		return
	}
	if _, err := s.repo.GetNewsletter(r.Context(), user.WorkspaceID, accountID, jid); err != nil {
		writeAppError(w, err)
		return
	}

	in := wa.NewsletterPostInput{
		AccountID: accountID,
		JID:       jid,
		Kind:      req.Kind,
		Text:      req.Text,
	}
	if req.Kind == wa.PostPoll {
		in.PollName = req.Text
		in.PollOptions = req.Options
		in.PollSelectableCount = req.SelectableCount
	}

	s.publishToChannel(w, r, accountID, jid, in)
}

// handlePostNewsletterMedia publishes one file to a channel we run.
//
// Built on the same validator as the chat upload: the file is classified from
// its own leading bytes, executables are refused however they are named, and the
// per-kind size ceiling is checked before a single byte reaches WhatsApp. The
// temporary file never outlives the request.
//
// Nothing is written to our own bucket, unlike a chat attachment. A channel post
// is read back from WhatsApp, so a copy here would be a file nothing ever reads.
func (s *Server) handlePostNewsletterMedia(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Permintaan harus berupa multipart/form-data")
		return
	}

	var (
		jid        string
		caption    string
		wantKind   string
		fileName   string
		declared   string
		asDocument bool
		temp       *os.File
		size       int64
	)
	defer func() {
		if temp != nil {
			name := temp.Name()
			_ = temp.Close()
			_ = os.Remove(name)
		}
	}()

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeUploadError(w, err)
			return
		}
		switch part.FormName() {
		case "jid":
			jid = strings.TrimSpace(readField(part, 256))
		case "caption":
			caption = readField(part, 8<<10)
		case "kind":
			wantKind = strings.TrimSpace(readField(part, 32))
		case "as_document":
			asDocument, _ = strconv.ParseBool(readField(part, 16))
		case "file":
			if temp != nil {
				_ = part.Close()
				continue // only the first file part is honoured
			}
			fileName = part.FileName()
			declared = part.Header.Get("Content-Type")
			temp, err = os.CreateTemp("", "salesan-ch-*")
			if err != nil {
				_ = part.Close()
				writeError(w, http.StatusInternalServerError, "internal_error",
					"Tidak bisa menyiapkan berkas sementara")
				return
			}
			size, err = io.Copy(temp, part)
			_ = part.Close()
			if err != nil {
				writeUploadError(w, err)
				return
			}
		default:
			_ = part.Close()
		}
	}

	if jid == "" {
		writeError(w, http.StatusBadRequest, "missing_channel", "Saluran tidak disebutkan")
		return
	}
	if temp == nil {
		writeError(w, http.StatusBadRequest, "missing_file", "Berkas tidak ditemukan pada permintaan")
		return
	}
	if _, err := s.repo.GetNewsletter(r.Context(), user.WorkspaceID, accountID, jid); err != nil {
		writeAppError(w, err)
		return
	}

	head := make([]byte, 512)
	n, _ := temp.ReadAt(head, 0)
	head = head[:n]
	if fileName == "" {
		fileName = "berkas"
	}
	file, err := media.Classify(fileName, declared, head, size, asDocument)
	if err != nil {
		writeMediaValidationError(w, err)
		return
	}

	// The kind the file turned out to be, unless the operator asked for a
	// sticker. Only a sticker is a request rather than an observation: the same
	// WebP is a picture or a sticker depending on which button was pressed, and
	// the manager refuses the request when the bytes are not a WebP.
	kind := string(file.Kind)
	if wantKind == wa.PostSticker {
		kind = wa.PostSticker
	}

	s.publishToChannel(w, r, accountID, jid, wa.NewsletterPostInput{
		AccountID: accountID,
		JID:       jid,
		Kind:      kind,
		Text:      caption,
		File:      &file,
		Source:    temp,
	})
}

// publishToChannel sends the post and answers with the channel as it stands
// afterwards.
//
// The posts come back from WhatsApp rather than from the request, because that
// is the only place they live — nothing about a channel post is stored here.
// WhatsApp can take a moment to list a post it has just accepted, so the reply
// also carries the timestamp of the send itself: the interface says "terkirim"
// from that, not from finding the post in the list.
func (s *Server) publishToChannel(
	w http.ResponseWriter,
	r *http.Request,
	accountID uuid.UUID,
	jid string,
	in wa.NewsletterPostInput,
) {
	postedAt, err := s.manager.PostToNewsletter(r.Context(), in)
	if err != nil {
		writeAppError(w, err)
		return
	}

	posts, err := s.manager.NewsletterPosts(r.Context(), accountID, jid, 30)
	if err != nil {
		// It went out. Failing the request now would tell the operator the post
		// did not happen, and they would send it again.
		writeJSON(w, http.StatusCreated, map[string]any{"posted_at": postedAt})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"posted_at": postedAt, "posts": posts})
}

// handleNewsletterAction performs the four things a subscriber can do.
func (s *Server) handleNewsletterAction(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	accountID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, accountID); err != nil {
		writeAppError(w, err)
		return
	}

	var req struct {
		// Ref is a channel address, or the invite link somebody pasted.
		Ref    string `json:"ref"`
		Action string `json:"action"`
		Muted  bool   `json:"muted"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		writeError(w, http.StatusBadRequest, "missing_channel", "Saluran tidak disebutkan")
		return
	}

	switch req.Action {
	case "preview":
		found, err := s.manager.PreviewNewsletter(r.Context(), accountID, ref)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"newsletter": found})
		return

	case "follow":
		// A link is resolved to an address first: FollowNewsletter needs a JID,
		// and the thing people paste is a link.
		target := ref
		if !strings.Contains(target, "@") {
			found, err := s.manager.PreviewNewsletter(r.Context(), accountID, ref)
			if err != nil {
				writeAppError(w, err)
				return
			}
			target = found.JID
		}
		if err := s.manager.FollowNewsletter(r.Context(), accountID, target); err != nil {
			writeAppError(w, err)
			return
		}

	case "unfollow":
		if err := s.manager.UnfollowNewsletter(r.Context(), accountID, ref); err != nil {
			writeAppError(w, err)
			return
		}

	case "mute":
		if err := s.manager.MuteNewsletter(r.Context(), accountID, ref, req.Muted); err != nil {
			writeAppError(w, err)
			return
		}

	default:
		writeError(w, http.StatusBadRequest, "invalid_action",
			"Aksi saluran harus preview, follow, unfollow, atau mute")
		return
	}

	list, err := s.repo.ListNewsletters(r.Context(), user.WorkspaceID, accountID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"newsletters": list})
}
