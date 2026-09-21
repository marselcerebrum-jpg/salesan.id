// Package auth verifies Supabase Auth access tokens.
//
// Supabase projects sign JWTs either with the legacy shared secret (HS256) or
// with an asymmetric key published through JWKS (ES256/RS256). Both are
// supported: JWKS is preferred when a URL is configured, with the shared secret
// as the fallback, so the same binary works on old and new projects.
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the subset of the Supabase token we care about.
type Claims struct {
	UserID uuid.UUID
	Email  string
	Role   string
}

var (
	// ErrNoToken is returned when the request carried no bearer token.
	ErrNoToken = errors.New("auth: missing token")
	// ErrInvalidToken is returned for any token that fails verification.
	ErrInvalidToken = errors.New("auth: invalid token")
)

const jwksTTL = 10 * time.Minute

// Verifier validates access tokens. It is safe for concurrent use.
type Verifier struct {
	hsSecret []byte
	jwksURL  string
	audience string
	client   *http.Client

	mu        sync.RWMutex
	keys      map[string]any
	fetchedAt time.Time
}

// NewVerifier builds a Verifier. At least one of secret or jwksURL must be set;
// config.Load already enforces that.
func NewVerifier(secret, jwksURL, audience string) *Verifier {
	return &Verifier{
		hsSecret: []byte(secret),
		jwksURL:  jwksURL,
		audience: audience,
		client:   &http.Client{Timeout: 10 * time.Second},
		keys:     map[string]any{},
	}
}

// Verify parses and validates a raw JWT, returning its claims.
func (v *Verifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, ErrNoToken
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256", "ES256", "ES384", "RS256", "RS512"}),
		jwt.WithLeeway(30*time.Second),
	)

	token, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
		switch t.Method.Alg() {
		case "HS256":
			if len(v.hsSecret) == 0 {
				return nil, errors.New("HS256 token but no SUPABASE_JWT_SECRET configured")
			}
			return v.hsSecret, nil
		default:
			kid, _ := t.Header["kid"].(string)
			return v.publicKey(ctx, kid)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}

	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalidToken
	}

	// Supabase encodes `aud` as a string; tolerate the array form too.
	if v.audience != "" && !audienceMatches(mc["aud"], v.audience) {
		return nil, fmt.Errorf("%w: unexpected audience", ErrInvalidToken)
	}

	sub, _ := mc["sub"].(string)
	userID, err := uuid.Parse(sub)
	if err != nil {
		return nil, fmt.Errorf("%w: sub is not a uuid", ErrInvalidToken)
	}

	email, _ := mc["email"].(string)
	role, _ := mc["role"].(string)
	return &Claims{UserID: userID, Email: email, Role: role}, nil
}

func audienceMatches(raw any, want string) bool {
	switch v := raw.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	case nil:
		// Some self-hosted setups omit `aud` entirely.
		return true
	}
	return false
}

// publicKey resolves a `kid` against the cached JWKS, refetching when the cache
// is stale or the key is unknown (Supabase rotates keys without warning).
func (v *Verifier) publicKey(ctx context.Context, kid string) (any, error) {
	if v.jwksURL == "" {
		return nil, errors.New("asymmetric token but no JWKS URL configured")
	}

	v.mu.RLock()
	key, found := v.keys[kid]
	fresh := time.Since(v.fetchedAt) < jwksTTL
	v.mu.RUnlock()
	if found && fresh {
		return key, nil
	}

	if err := v.refresh(ctx); err != nil {
		// A stale-but-present key beats failing the request outright.
		if found {
			return key, nil
		}
		return nil, err
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	// A JWKS with exactly one key and a token without `kid` is common.
	if kid == "" && len(v.keys) == 1 {
		for _, only := range v.keys {
			return only, nil
		}
	}
	return nil, fmt.Errorf("no JWKS key matches kid %q", kid)
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (v *Verifier) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: unexpected status %d", resp.StatusCode)
	}

	var set jwkSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	parsed := make(map[string]any, len(set.Keys))
	for _, k := range set.Keys {
		key, err := k.publicKey()
		if err != nil {
			continue // skip keys we cannot represent rather than failing the set
		}
		parsed[k.Kid] = key
	}
	if len(parsed) == 0 {
		return errors.New("jwks contained no usable keys")
	}

	v.mu.Lock()
	v.keys = parsed
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

func (k jwk) publicKey() (any, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64uint(k.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.E, "="))
		if err != nil {
			return nil, err
		}
		// `e` is big-endian and usually 3 bytes; left-pad to 8 for Uint64.
		padded := make([]byte, 8)
		copy(padded[8-len(eBytes):], eBytes)
		return &rsa.PublicKey{N: n, E: int(binary.BigEndian.Uint64(padded))}, nil

	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve %q", k.Crv)
		}
		x, err := b64uint(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64uint(k.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil

	default:
		return nil, fmt.Errorf("unsupported key type %q", k.Kty)
	}
}

func b64uint(s string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(raw), nil
}

// BearerToken pulls the token out of an Authorization header value.
func BearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
