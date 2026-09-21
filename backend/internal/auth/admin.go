package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Admin creates and manages Supabase Auth users on behalf of a workspace.
//
// It speaks the Supabase Admin API, which requires the service-role key. That
// key bypasses RLS entirely, so it never leaves this process: no handler
// forwards it, no response carries it, and every call it makes is one this
// server decided to make after checking the caller's role itself.
type Admin struct {
	baseURL string
	key     string
	http    *http.Client
}

var (
	// ErrAdminUnavailable reports that no service-role key is configured, so
	// accounts cannot be created from here. Deliberately distinct from a
	// failure: nothing is wrong, a setting is simply missing.
	ErrAdminUnavailable = errors.New("auth: SUPABASE_SERVICE_ROLE_KEY is not configured")
	// ErrEmailTaken reports that the address already belongs to a user.
	ErrEmailTaken = errors.New("auth: email already registered")
	// ErrWeakPassword reports a password Supabase refused.
	ErrWeakPassword = errors.New("auth: password rejected")
)

// NewAdmin builds a client. It is usable even without a key: Enabled reports
// false and every call returns ErrAdminUnavailable, which lets the rest of the
// server be written without nil checks.
func NewAdmin(supabaseURL, serviceRoleKey string) *Admin {
	return &Admin{
		baseURL: strings.TrimRight(supabaseURL, "/"),
		key:     strings.TrimSpace(serviceRoleKey),
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Enabled reports whether account creation is possible.
func (a *Admin) Enabled() bool {
	return a != nil && a.baseURL != "" && a.key != ""
}

// CreateUserInput describes one account to create.
type CreateUserInput struct {
	Email    string
	Password string
	FullName string
	// WorkspaceID is written into the user's metadata, where the signup trigger
	// reads it to place the profile in an existing workspace rather than
	// creating a fresh one. See migration 0025.
	WorkspaceID uuid.UUID
	// WorkspaceRole is the ownership role inside that workspace. "owner" is
	// downgraded by the trigger; ownership is not handed over by invitation.
	WorkspaceRole string
}

type adminUserResponse struct {
	ID  string `json:"id"`
	Msg string `json:"msg"`
	// Supabase reports failures under several names depending on the version.
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Message          string `json:"message"`
	Code             string `json:"error_code"`
}

// CreateUser creates a confirmed Supabase account bound to a workspace.
//
// The address is confirmed on creation: this is a team member being set up by
// their Leader, not a stranger signing up, and leaving them unable to log in
// until they find a confirmation email would make the feature useless in the
// room where it is actually used.
func (a *Admin) CreateUser(ctx context.Context, in CreateUserInput) (uuid.UUID, error) {
	if !a.Enabled() {
		return uuid.Nil, ErrAdminUnavailable
	}

	body, err := json.Marshal(map[string]any{
		"email":         in.Email,
		"password":      in.Password,
		"email_confirm": true,
		"user_metadata": map[string]any{
			"full_name":              in.FullName,
			"salesan_workspace_id":   in.WorkspaceID.String(),
			"salesan_workspace_role": in.WorkspaceRole,
		},
	})
	if err != nil {
		return uuid.Nil, err
	}

	parsed, err := a.call(ctx, http.MethodPost, "/auth/v1/admin/users", body)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(parsed.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase admin returned no user id")
	}
	return id, nil
}

// SetPassword replaces an existing account's password.
//
// No current password is asked for: this is the Leader (or the Freelance's own
// PIC) resetting a teammate who forgot theirs, which is the case the member
// screen exists for. Who may do it is decided by the caller before this runs.
// The password itself is sent to Supabase and nowhere else — it is not logged,
// stored, or echoed back.
func (a *Admin) SetPassword(ctx context.Context, userID uuid.UUID, password string) error {
	if !a.Enabled() {
		return ErrAdminUnavailable
	}
	body, err := json.Marshal(map[string]any{"password": password})
	if err != nil {
		return err
	}
	_, err = a.call(ctx, http.MethodPut, "/auth/v1/admin/users/"+userID.String(), body)
	return err
}

// call sends one Admin API request and turns Supabase's several error shapes
// into the sentinels above.
func (a *Admin) call(ctx context.Context, method, path string, body []byte) (*adminUserResponse, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", a.key)
	req.Header.Set("Authorization", "Bearer "+a.key)

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("supabase admin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var parsed adminUserResponse
	_ = json.Unmarshal(raw, &parsed)

	if resp.StatusCode >= 400 {
		detail := firstNonEmpty(parsed.Msg, parsed.Message, parsed.ErrorDescription, parsed.Error)
		lower := strings.ToLower(detail + " " + parsed.Code)
		switch {
		case strings.Contains(lower, "already been registered"),
			strings.Contains(lower, "already registered"),
			strings.Contains(lower, "email_exists"),
			strings.Contains(lower, "duplicate"):
			return nil, ErrEmailTaken
		case strings.Contains(lower, "password"):
			return nil, fmt.Errorf("%w: %s", ErrWeakPassword, detail)
		}
		if detail == "" {
			detail = strings.TrimSpace(string(raw))
		}
		return nil, fmt.Errorf("supabase admin (%d): %s", resp.StatusCode, detail)
	}
	return &parsed, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
