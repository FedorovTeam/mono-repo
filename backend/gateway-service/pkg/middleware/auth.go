package middleware

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// OIDCValidator валидирует JWT по JWKS.
type OIDCValidator struct {
	keyfunc  keyfunc.Keyfunc
	issuer   string
	audience string
	now      func() time.Time
}

// OIDCConfig описывает настройки валидации токена.
type OIDCConfig struct {
	JWKSURL  string
	Issuer   string
	Audience string

	RefreshInterval time.Duration
	RefreshTimeout  time.Duration
	HTTPClient      *http.Client
}

// NewOIDCValidator создаёт валидатор с кешированием JWKS.
func NewOIDCValidator(ctx context.Context, cfg OIDCConfig, logger *zap.Logger) (*OIDCValidator, error) {
	if cfg.JWKSURL == "" {
		return nil, errors.New("jwks url is required")
	}
	if cfg.Issuer == "" {
		return nil, errors.New("issuer is required")
	}
	if cfg.Audience == "" {
		return nil, errors.New("audience is required")
	}

	refreshInterval := cfg.RefreshInterval
	if refreshInterval == 0 {
		refreshInterval = 10 * time.Minute
	}
	refreshTimeout := cfg.RefreshTimeout
	if refreshTimeout == 0 {
		refreshTimeout = 5 * time.Second
	}

	override := keyfunc.Override{
		Client:          cfg.HTTPClient,
		HTTPTimeout:     refreshTimeout,
		RefreshInterval: refreshInterval,
		RefreshErrorHandlerFunc: func(u string) func(context.Context, error) {
			return func(ctx context.Context, err error) {
				logger.Warn("failed to refresh jwks", zap.Error(err), zap.String("jwks_url", u))
			}
		},
	}
	jwks, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{cfg.JWKSURL}, override)
	if err != nil {
		return nil, fmt.Errorf("failed to init jwks: %w", err)
	}

	return &OIDCValidator{
		keyfunc:  jwks,
		issuer:   cfg.Issuer,
		audience: cfg.Audience,
		now:      time.Now,
	}, nil
}

// ValidateToken проверяет подпись и возвращает данные пользователя.
func (v *OIDCValidator) ValidateToken(ctx context.Context, rawToken string) (*ctxutils.User, error) {
	if v == nil {
		return nil, errors.New("validator is not configured")
	}

	var claims oidcClaims
	token, err := jwt.ParseWithClaims(rawToken, &claims, v.keyfunc.KeyfuncCtx(ctx),
		jwt.WithAudience(v.audience),
		jwt.WithIssuer(v.issuer),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to validate token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("token is not valid")
	}
	if claims.Subject == "" {
		return nil, errors.New("token subject is empty")
	}

	return &ctxutils.User{
		ID:     claims.Subject,
		Email:  claims.Email,
		Groups: claims.Groups,
	}, nil
}

type oidcClaims struct {
	Email  string   `json:"email"`
	Groups []string `json:"groups"`
	jwt.RegisteredClaims
}

// AuthenticationMiddleware валидирует Bearer токен OIDC или X-API-Key (если включен).
func AuthenticationMiddleware(validator *OIDCValidator, apiKey string, apiKeyEnabled bool, logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			authorization := r.Header.Get("Authorization")
			if strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
				token := strings.TrimSpace(authorization[7:])
				if validator == nil {
					logger.Error("oidc validator is not configured")
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}

				user, err := validator.ValidateToken(r.Context(), token)
				if err != nil {
					logger.Warn("invalid bearer token", zap.Error(err), zap.String("path", r.URL.Path))
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}

				ctx := ctxutils.WithUser(r.Context(), *user)
				ctx = ctxutils.WithLogFields(ctx, map[string]string{
					"correlation_id": ctxutils.CorrelationID(ctx),
					"user_id":        user.ID,
					"user_email":     user.Email,
				})

				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if apiKeyEnabled {
				if apiKey == "" {
					http.Error(w, "api key is not configured", http.StatusInternalServerError)
					return
				}
				apiKeyHeader := r.Header.Get("X-API-Key")
				if apiKeyHeader != apiKey {
					logger.Warn("unauthorized request", zap.String("path", r.URL.Path))
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				ctx := ctxutils.WithAPIKey(r.Context(), apiKeyHeader)
				ctx = ctxutils.WithLogFields(ctx, map[string]string{"correlation_id": ctxutils.CorrelationID(ctx), "api_key": apiKeyHeader})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			logger.Warn("unauthorized request without auth header", zap.String("path", r.URL.Path))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}
