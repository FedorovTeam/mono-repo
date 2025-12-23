package middleware

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

func TestAuthenticationMiddlewareAPIKey(t *testing.T) {
	logger := zap.NewNop()
	handler := AuthenticationMiddleware(nil, "secret", true, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ctxutils.APIKey(r.Context()) != "secret" {
			t.Fatalf("api key was not stored in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthenticationMiddlewareBearerToken(t *testing.T) {
	validator, token := newTestValidator(t)
	handler := AuthenticationMiddleware(validator, "", false, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := ctxutils.UserFromContext(r.Context())
		if user == nil || user.ID != "user-1" || user.Email != "user@example.com" {
			t.Fatalf("user not stored in context: %+v", user)
		}
		if len(user.Groups) != 2 || user.Groups[0] != "admins" {
			t.Fatalf("groups not stored correctly: %+v", user.Groups)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestCorrelationIDMiddleware(t *testing.T) {
	logger := zap.NewNop()
	var captured string

	handler := CorrelationIDMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = ctxutils.CorrelationID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Correlation-ID", "corr-123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if captured != "corr-123" {
		t.Fatalf("expected correlation id to propagate, got %s", captured)
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
}

func newTestValidator(t *testing.T) (*OIDCValidator, string) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	kid := "test-key"
	n := base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes())
	jwks := `{"keys":[{"kty":"RSA","kid":"` + kid + `","use":"sig","alg":"RS256","n":"` + n + `","e":"` + e + `"}]}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(jwks))
	}))
	t.Cleanup(ts.Close)

	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{
		JWKSURL:  ts.URL,
		Issuer:   "https://issuer.test/",
		Audience: "audience",
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	claims := struct {
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
		jwt.RegisteredClaims
	}{
		Email:  "user@example.com",
		Groups: []string{"admins", "editors"},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			Issuer:    "https://issuer.test/",
			Audience:  jwt.ClaimStrings{"audience"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	tokenStr, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	return validator, tokenStr
}
