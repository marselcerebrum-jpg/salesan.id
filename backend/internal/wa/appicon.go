package wa

import (
	"context"
	"io"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/media"
)

// Application logos.
//
// Stored in the private bucket like every other file, under a key built from
// ids the server controls. The database keeps the key; the browser only ever
// receives a signed link that expires, minted when the list is read.

// MaxAppIconBytes caps one logo. A logo is drawn at 26–44 pixels; two megabytes
// is generous for that, and anything larger is almost certainly the wrong file.
const MaxAppIconBytes = 2 << 20

// appIconPrefix marks a stored key as ours. An icon_url that does not start
// with it is left exactly as it is: it predates uploads, and signing it as a
// key would turn a working value into a broken one.
const appIconPrefix = "app-icons/"

// UploadAppIcon stores one validated logo and returns its key.
func (m *Manager) UploadAppIcon(
	ctx context.Context, workspaceID, applicationID uuid.UUID, file media.File, src io.Reader, size int64,
) (string, error) {
	if m.store == nil {
		return "", ErrMediaDisabled
	}
	key := AppIconKey(workspaceID, applicationID, file.MIME)
	if err := m.store.Upload(ctx, key, file.MIME, src, size); err != nil {
		return "", err
	}
	return key, nil
}

// AppIconKey is where one logo lives. Built only from ids the server controls,
// and a new name every upload, so a browser holding the old signed link never
// shows the new logo as the old one, or the reverse.
func AppIconKey(workspaceID, applicationID uuid.UUID, mime string) string {
	return path.Join(appIconPrefix+workspaceID.String(), applicationID.String(),
		uuid.NewString()+media.ExtensionForMIME(mime))
}

// IsAppIconKey reports whether a stored value is a logo key this code wrote.
func IsAppIconKey(value string) bool { return strings.HasPrefix(value, appIconPrefix) }

// RemoveAppIcon deletes a stored logo. Only keys this code wrote are touched.
func (m *Manager) RemoveAppIcon(ctx context.Context, key string) {
	if m.store == nil || !IsAppIconKey(key) {
		return
	}
	m.removeStoredObjects(ctx, []string{key})
}

// AppIconURL turns a stored logo key into a short-lived link. Anything that is
// not one of our keys is returned unchanged.
func (m *Manager) AppIconURL(ctx context.Context, value string) string {
	if !strings.HasPrefix(value, appIconPrefix) {
		return value
	}
	if m.store == nil {
		return ""
	}
	signed, err := m.store.SignedURL(ctx, value, m.cfg.MediaURLTTL, "")
	if err != nil {
		return ""
	}
	return signed
}
