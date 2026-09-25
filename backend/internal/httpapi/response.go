// Package httpapi exposes the REST + WebSocket surface consumed by the Next.js
// frontend. Handlers stay thin: they parse input, delegate to the repository or
// the WhatsApp manager, and serialise the result.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

const maxBodyBytes = 1 << 20 // 1 MiB

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	// Data carries the structured part of a rejection, for the cases where a
	// sentence is not enough — the composer needs the list of bad recipients,
	// not just "some recipients are invalid".
	Data any `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: code, Message: message})
}

// writeErrorWithData is writeError plus the machine-readable detail behind it.
func writeErrorWithData(w http.ResponseWriter, status int, code, message string, data any) {
	writeJSON(w, status, errorBody{Error: code, Message: message, Data: data})
}

// writeAppError maps domain errors onto HTTP status codes so every handler
// reports failures the same way.
func writeAppError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Data tidak ditemukan")
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden",
			"Data ini berada di luar aplikasi yang ditugaskan kepada akun Anda")
	case errors.Is(err, wa.ErrSessionNotFound):
		writeError(w, http.StatusConflict, "session_not_found",
			"Akun ini belum terhubung. Hubungkan perangkat terlebih dahulu.")
	case errors.Is(err, wa.ErrNotConnected):
		writeError(w, http.StatusConflict, "not_connected",
			"Akun WhatsApp sedang tidak terhubung.")
	case errors.Is(err, wa.ErrAlreadyPaired):
		writeError(w, http.StatusConflict, "already_paired",
			"Akun sudah tertaut. Putuskan koneksi dulu untuk memindai QR baru.")
	case errors.Is(err, wa.ErrEmptyMessage):
		writeError(w, http.StatusBadRequest, "empty_message", "Pesan tidak boleh kosong")
	case errors.Is(err, wa.ErrMediaDisabled):
		writeError(w, http.StatusServiceUnavailable, "media_disabled",
			"Penyimpanan media belum dikonfigurasi pada server.")
	case errors.Is(err, wa.ErrMediaUnavailable):
		writeError(w, http.StatusGone, "media_unavailable",
			"Berkas ini sudah tidak tersedia di server WhatsApp.")
	// Deliberately without a number of days. The window is a server setting, and
	// a sentence that names it here goes quietly wrong the moment it is changed.
	// What the reader needs is not the policy but where the file still is.
	case errors.Is(err, wa.ErrMediaExpired):
		writeError(w, http.StatusGone, "media_expired",
			"Berkas ini sudah dihapus dari server. Silakan cek di HP.")
	// 409, not 410. The file is coming; the browser should wait and ask again
	// rather than draw a dead end over a message that was sent successfully.
	case errors.Is(err, wa.ErrMediaNotReady):
		writeError(w, http.StatusConflict, "media_not_ready",
			"Berkas masih diunggah. Tunggu sebentar.")
	case errors.Is(err, wa.ErrNotEditable):
		writeError(w, http.StatusConflict, "not_editable", detailAfter(err, wa.ErrNotEditable))
	case errors.Is(err, wa.ErrNotAGroup):
		writeError(w, http.StatusBadRequest, "not_a_group",
			"Tindakan ini hanya berlaku untuk grup.")
	case errors.Is(err, wa.ErrNotGroupAdmin):
		writeError(w, http.StatusForbidden, "not_group_admin", detailAfter(err, wa.ErrNotGroupAdmin))
	case errors.Is(err, wa.ErrNotForwardable):
		writeError(w, http.StatusConflict, "not_forwardable", detailAfter(err, wa.ErrNotForwardable))
	case errors.Is(err, wa.ErrNotRevocable):
		writeError(w, http.StatusConflict, "not_revocable", detailAfter(err, wa.ErrNotRevocable))
	case errors.Is(err, wa.ErrInvalidReaction):
		writeError(w, http.StatusBadRequest, "invalid_reaction", detailAfter(err, wa.ErrInvalidReaction))
	case errors.Is(err, wa.ErrChannelNotAdmin):
		writeError(w, http.StatusForbidden, "not_channel_admin",
			"Nomor ini bukan pemilik atau admin saluran tersebut, jadi tidak bisa memposting ke sana.")
	case errors.Is(err, wa.ErrChannelSend):
		writeError(w, http.StatusBadGateway, "channel_send_failed",
			"WhatsApp menolak postingan: "+detailAfter(err, wa.ErrChannelSend))
	case errors.Is(err, wa.ErrInvalidPost):
		writeError(w, http.StatusBadRequest, "invalid_post", detailAfter(err, wa.ErrInvalidPost))
	case errors.Is(err, wa.ErrInvalidPoll):
		// The sentinel's own text is a package-level label, not something to
		// show an operator; only the detail after it is worth reading.
		writeError(w, http.StatusBadRequest, "invalid_poll", detailAfter(err, wa.ErrInvalidPoll))
	default:
		slog.Error("unhandled api error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error",
			"Terjadi kesalahan pada server")
	}
}

// detailAfter strips a wrapped sentinel's own message, leaving the part that
// explains what the caller did wrong.
//
// Errors are built as `fmt.Errorf("%w: minimal 2 pilihan", ErrInvalidPoll)`,
// which reads well in a log and badly in a dialog — "wa: poll is not valid:
// minimal 2 pilihan" tells the operator nothing the second half does not.
func detailAfter(err, sentinel error) string {
	full := err.Error()
	prefix := sentinel.Error() + ": "
	if idx := strings.Index(full, prefix); idx >= 0 {
		if detail := strings.TrimSpace(full[idx+len(prefix):]); detail != "" {
			return capitalizeFirst(detail)
		}
	}
	return full
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_body", "Body permintaan kosong")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		}
		return false
	}
	return true
}

// decodeJSONOptional reads a body that may legitimately be absent.
//
// For requests whose every field is optional: an empty body means "no fields",
// which is a valid request rather than a malformed one. Unknown fields are
// still refused, so a typo is not silently ignored.
func decodeJSONOptional(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func parseUUIDParam(w http.ResponseWriter, raw, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", name+" tidak valid")
		return uuid.Nil, false
	}
	return id, true
}

func queryUUID(r *http.Request, key string) *uuid.UUID {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func queryBool(r *http.Request, key string) bool {
	b, _ := strconv.ParseBool(r.URL.Query().Get(key))
	return b
}

func queryTime(r *http.Request, key string) *time.Time {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil
	}
	return &t
}

// oneOf returns value when it is in allowed, otherwise "".
func oneOf(value string, allowed ...string) string {
	for _, a := range allowed {
		if value == a {
			return value
		}
	}
	return ""
}
