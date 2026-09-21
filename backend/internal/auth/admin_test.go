package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Without a service-role key the feature is unavailable, not broken. The
// difference matters: one is a setting the operator can fill in, the other is
// something to investigate.
func TestAdminWithoutKeyIsUnavailable(t *testing.T) {
	a := NewAdmin("https://project.supabase.co", "")
	if a.Enabled() {
		t.Fatal("Enabled() must be false without a key")
	}
	if _, err := a.CreateUser(context.Background(), CreateUserInput{}); !errors.Is(err, ErrAdminUnavailable) {
		t.Fatalf("err = %v, want ErrAdminUnavailable", err)
	}
}

func TestAdminWithoutURLIsUnavailable(t *testing.T) {
	if NewAdmin("", "service-key").Enabled() {
		t.Fatal("a key without a project URL is not usable")
	}
}

// The workspace id has to reach the signup trigger through user metadata; that
// is the whole mechanism by which a new member joins an existing team rather
// than landing in an empty workspace of their own.
func TestCreateUserSendsWorkspaceMetadata(t *testing.T) {
	workspace := uuid.New()
	created := uuid.New()

	var got map[string]any
	var auth, apikey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/admin/users" {
			t.Errorf("path = %q", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		apikey = r.Header.Get("apikey")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + created.String() + `"}`))
	}))
	defer srv.Close()

	id, err := NewAdmin(srv.URL, "service-key").CreateUser(context.Background(), CreateUserInput{
		Email:         "pic@example.com",
		Password:      "rahasia-panjang",
		FullName:      "PIC Satu",
		WorkspaceID:   workspace,
		WorkspaceRole: "agent",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id != created {
		t.Errorf("id = %s, want %s", id, created)
	}

	if auth != "Bearer service-key" || apikey != "service-key" {
		t.Errorf("service-role key not sent: authorization=%q apikey=%q", auth, apikey)
	}
	if got["email_confirm"] != true {
		t.Error("email_confirm must be true; an invited member has to be able to log in")
	}

	meta, _ := got["user_metadata"].(map[string]any)
	if meta["salesan_workspace_id"] != workspace.String() {
		t.Errorf("workspace metadata = %v, want %s", meta["salesan_workspace_id"], workspace)
	}
	if meta["salesan_workspace_role"] != "agent" {
		t.Errorf("role metadata = %v", meta["salesan_workspace_role"])
	}
}

func TestCreateUserMapsDuplicateEmail(t *testing.T) {
	// Supabase has used several shapes for this; all of them have to land on
	// one error the interface can explain in plain language.
	bodies := []string{
		`{"msg":"A user with this email address has already been registered"}`,
		`{"message":"duplicate key value violates unique constraint"}`,
		`{"error_code":"email_exists","message":"Email address already in use"}`,
	}
	for _, body := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(body))
		}))

		_, err := NewAdmin(srv.URL, "k").CreateUser(context.Background(), CreateUserInput{})
		srv.Close()

		if !errors.Is(err, ErrEmailTaken) {
			t.Errorf("body %s: err = %v, want ErrEmailTaken", body, err)
		}
	}
}

func TestCreateUserSurfacesUnknownFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"database is on fire"}`))
	}))
	defer srv.Close()

	_, err := NewAdmin(srv.URL, "k").CreateUser(context.Background(), CreateUserInput{})
	if err == nil || !strings.Contains(err.Error(), "database is on fire") {
		t.Fatalf("err = %v, want the upstream detail preserved", err)
	}
}
