package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "super-secret-jwt-value-for-tests"

func signHS256(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestVerifyHS256(t *testing.T) {
	userID := uuid.New()
	verifier := NewVerifier(testSecret, "", "authenticated")

	raw := signHS256(t, jwt.MapClaims{
		"sub":   userID.String(),
		"email": "arik@salesan.id",
		"role":  "authenticated",
		"aud":   "authenticated",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})

	claims, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("UserID = %v, want %v", claims.UserID, userID)
	}
	if claims.Email != "arik@salesan.id" {
		t.Errorf("Email = %q, want %q", claims.Email, "arik@salesan.id")
	}
}

func TestVerifyRejectsBadTokens(t *testing.T) {
	userID := uuid.New()
	verifier := NewVerifier(testSecret, "", "authenticated")

	tests := []struct {
		name    string
		token   string
		wantErr error
	}{
		{
			name:    "empty token",
			token:   "   ",
			wantErr: ErrNoToken,
		},
		{
			name:    "garbage",
			token:   "not-a-jwt",
			wantErr: ErrInvalidToken,
		},
		{
			name: "expired",
			token: signHS256(t, jwt.MapClaims{
				"sub": userID.String(),
				"aud": "authenticated",
				"exp": time.Now().Add(-2 * time.Hour).Unix(),
			}),
			wantErr: ErrInvalidToken,
		},
		{
			name: "wrong audience",
			token: signHS256(t, jwt.MapClaims{
				"sub": userID.String(),
				"aud": "anon",
				"exp": time.Now().Add(time.Hour).Unix(),
			}),
			wantErr: ErrInvalidToken,
		},
		{
			name: "sub is not a uuid",
			token: signHS256(t, jwt.MapClaims{
				"sub": "12345",
				"aud": "authenticated",
				"exp": time.Now().Add(time.Hour).Unix(),
			}),
			wantErr: ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), tt.token)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Verify() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyRejectsAlgNone(t *testing.T) {
	// A token signed with "none" must never be accepted, even though the
	// library can technically parse it.
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": uuid.New().String(),
		"aud": "authenticated",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	raw, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}

	verifier := NewVerifier(testSecret, "", "authenticated")
	if _, err := verifier.Verify(context.Background(), raw); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("alg=none accepted, error = %v", err)
	}
}

func TestAudienceMatches(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want bool
	}{
		{"string match", "authenticated", true},
		{"string mismatch", "anon", false},
		{"array containing match", []any{"anon", "authenticated"}, true},
		{"array without match", []any{"anon"}, false},
		{"absent claim is tolerated", nil, true},
		{"unexpected type", 42, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := audienceMatches(tt.raw, "authenticated"); got != tt.want {
				t.Errorf("audienceMatches(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", "abc123"},
		{"BEARER  abc123 ", "abc123"},
		{"Basic abc123", ""},
		{"", ""},
		{"Bearer", ""},
	}

	for _, tt := range tests {
		if got := BearerToken(tt.header); got != tt.want {
			t.Errorf("BearerToken(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}
