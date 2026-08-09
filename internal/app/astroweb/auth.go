package astroweb

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	telegramIssuer               = "https://oauth.telegram.org"
	oidcTransactionCookie        = "__Host-astrosferum_oidc"
	sessionCookie                = "__Host-astrosferum_session"
	defaultTransactionLifetime   = 10 * time.Minute
	defaultSessionLifetime       = 30 * 24 * time.Hour
	maximumOIDCClockSkew         = 2 * time.Minute
	minimumOpaqueCredentialBytes = 32
)

var (
	ErrUnauthenticated      = errors.New("unauthenticated")
	ErrInvalidAuthState     = errors.New("invalid authentication state")
	ErrAuthTransactionStale = errors.New("authentication transaction expired")
	errInvalidCSRFRequest   = errors.New("invalid CSRF request")
)

// AuthTransaction contains the server-side half of one browser-bound OIDC
// authorization. HandleHash and StateHash are SHA-256 digests; the browser
// never receives the PKCE verifier or nonce.
type AuthTransaction struct {
	HandleHash   [sha256.Size]byte
	StateHash    [sha256.Size]byte
	PKCEVerifier string
	Nonce        string
	Language     string
	ExpiresAt    time.Time
}

// WebSession is an opaque, revocable server-side session. TokenHash is stored
// instead of the bearer value carried by the Secure/HttpOnly cookie.
type WebSession struct {
	TokenHash      [sha256.Size]byte
	TelegramUserID int64
	OIDCIssuer     string
	OIDCSubject    string
	Language       string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// AuthStore deliberately exposes only atomic authentication operations. The
// PostgreSQL adapter owns SQL and transaction details; the HTTP application
// never receives a database pool.
type AuthStore interface {
	CreateAuthTransaction(context.Context, AuthTransaction) error
	ConsumeAuthTransaction(context.Context, [sha256.Size]byte, [sha256.Size]byte, time.Time) (AuthTransaction, error)
	CreateWebSession(context.Context, WebSession) error
	WebSession(context.Context, [sha256.Size]byte, time.Time) (WebSession, error)
	UpdateWebSessionLanguage(context.Context, [sha256.Size]byte, int64, string) error
	RevokeWebSession(context.Context, [sha256.Size]byte) error
}

type OIDCConfig struct {
	Issuer              string
	ClientID            string
	ClientSecret        string
	RedirectURL         string
	PublicOrigin        string
	TransactionLifetime time.Duration
	SessionLifetime     time.Duration
	CSRFKey             []byte
	HTTPClient          *http.Client
}

func (config OIDCConfig) validate() error {
	if config.Issuer == "" {
		config.Issuer = telegramIssuer
	}
	issuer, err := url.Parse(config.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.Path != "" || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("OIDC issuer must be an HTTPS origin")
	}
	if config.Issuer != telegramIssuer {
		return errors.New("only the Telegram OIDC issuer is supported")
	}
	clientID, err := strconv.ParseInt(strings.TrimSpace(config.ClientID), 10, 64)
	if err != nil || clientID <= 0 {
		return errors.New("OIDC client ID must be a positive Telegram bot ID")
	}
	if strings.TrimSpace(config.ClientSecret) == "" {
		return errors.New("OIDC client secret is required")
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" || redirect.RawQuery != "" || redirect.Fragment != "" {
		return errors.New("OIDC redirect URL must be an absolute HTTPS URL without query or fragment")
	}
	origin, err := url.Parse(config.PublicOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("public origin must be an HTTPS origin")
	}
	if redirect.Scheme != origin.Scheme || !strings.EqualFold(redirect.Host, origin.Host) {
		return errors.New("OIDC redirect URL must use the configured public origin")
	}
	if config.TransactionLifetime <= 0 || config.TransactionLifetime > 30*time.Minute {
		return errors.New("OIDC transaction lifetime must be between zero and 30 minutes")
	}
	if config.SessionLifetime <= 0 || config.SessionLifetime > 90*24*time.Hour {
		return errors.New("web session lifetime must be between zero and 90 days")
	}
	if len(config.CSRFKey) < minimumOpaqueCredentialBytes {
		return fmt.Errorf("CSRF key must contain at least %d bytes", minimumOpaqueCredentialBytes)
	}
	return nil
}

type OIDCAuthenticator struct {
	store        AuthStore
	oauth        oauth2.Config
	verifier     *oidc.IDTokenVerifier
	httpClient   *http.Client
	publicOrigin string
	transaction  time.Duration
	session      time.Duration
	csrfKey      []byte
	now          func() time.Time
	random       func([]byte) error
}

func NewOIDCAuthenticator(ctx context.Context, config OIDCConfig, store AuthStore) (*OIDCAuthenticator, error) {
	if store == nil {
		return nil, errors.New("authentication store is required")
	}
	if config.Issuer == "" {
		config.Issuer = telegramIssuer
	}
	if config.TransactionLifetime == 0 {
		config.TransactionLifetime = defaultTransactionLifetime
	}
	if config.SessionLifetime == 0 {
		config.SessionLifetime = defaultSessionLifetime
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	providerContext := ctx
	if config.HTTPClient != nil {
		providerContext = oidc.ClientContext(providerContext, config.HTTPClient)
	}
	provider, err := oidc.NewProvider(providerContext, config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover Telegram OIDC provider: %w", err)
	}
	return &OIDCAuthenticator{
		store: store,
		oauth: oauth2.Config{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL: provider.Endpoint().AuthURL, TokenURL: provider.Endpoint().TokenURL,
				AuthStyle: oauth2.AuthStyleInHeader,
			}, RedirectURL: config.RedirectURL,
			Scopes: []string{oidc.ScopeOpenID, "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{
			ClientID: config.ClientID, SupportedSigningAlgs: []string{oidc.RS256},
		}),
		httpClient: config.HTTPClient, publicOrigin: strings.TrimRight(config.PublicOrigin, "/"),
		transaction: config.TransactionLifetime, session: config.SessionLifetime,
		csrfKey: append([]byte(nil), config.CSRFKey...), now: time.Now,
		random: func(buffer []byte) error { _, err := rand.Read(buffer); return err },
	}, nil
}

// Begin starts Authorization Code + PKCE. It stores all sensitive transaction
// material server-side and sends only independent opaque state/cookie values
// to the browser.
func (auth *OIDCAuthenticator) Begin(w http.ResponseWriter, request *http.Request) error {
	handle, err := auth.opaqueCredential()
	if err != nil {
		return err
	}
	state, err := auth.opaqueCredential()
	if err != nil {
		return err
	}
	nonce, err := auth.opaqueCredential()
	if err != nil {
		return err
	}
	verifier, err := auth.opaqueCredentialWithBytes(48)
	if err != nil {
		return err
	}
	now := auth.now().UTC()
	transaction := AuthTransaction{
		HandleHash: sha256.Sum256([]byte(handle)), StateHash: sha256.Sum256([]byte(state)),
		PKCEVerifier: verifier, Nonce: nonce, Language: preferredLanguage(request),
		ExpiresAt: now.Add(auth.transaction),
	}
	if err := auth.store.CreateAuthTransaction(request.Context(), transaction); err != nil {
		return fmt.Errorf("store OIDC transaction: %w", err)
	}
	http.SetCookie(w, hostCookie(oidcTransactionCookie, handle, transaction.ExpiresAt, true))
	challenge := sha256.Sum256([]byte(verifier))
	authorizationURL := auth.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, request, authorizationURL, http.StatusSeeOther)
	return nil
}

// Callback consumes a browser-bound transaction exactly once, verifies the
// Telegram RS256 ID token, and replaces it with an application-owned session.
func (auth *OIDCAuthenticator) Callback(w http.ResponseWriter, request *http.Request) error {
	cookie, err := request.Cookie(oidcTransactionCookie)
	if err != nil || cookie.Value == "" {
		return ErrInvalidAuthState
	}
	state := request.URL.Query().Get("state")
	if state == "" {
		return ErrInvalidAuthState
	}
	transaction, err := auth.store.ConsumeAuthTransaction(
		request.Context(), sha256.Sum256([]byte(cookie.Value)), sha256.Sum256([]byte(state)), auth.now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAuthState, err)
	}
	clearHostCookie(w, oidcTransactionCookie)
	if providerError := request.URL.Query().Get("error"); providerError != "" {
		return errors.New("telegram authorization was rejected")
	}
	code := request.URL.Query().Get("code")
	if code == "" {
		return ErrInvalidAuthState
	}
	exchangeContext := request.Context()
	if auth.httpClient != nil {
		exchangeContext = oidc.ClientContext(exchangeContext, auth.httpClient)
	}
	token, err := auth.oauth.Exchange(exchangeContext, code,
		oauth2.SetAuthURLParam("code_verifier", transaction.PKCEVerifier),
		oauth2.SetAuthURLParam("client_id", auth.oauth.ClientID),
	)
	if err != nil {
		return fmt.Errorf("telegram authorization-code exchange failed: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return errors.New("telegram token response has no ID token")
	}
	idToken, err := auth.verifier.Verify(request.Context(), rawIDToken)
	if err != nil {
		return fmt.Errorf("telegram ID token verification failed: %w", err)
	}
	var claims struct {
		Subject         string         `json:"sub"`
		TelegramUserID  telegramUserID `json:"id"`
		Nonce           string         `json:"nonce"`
		AuthorizedParty string         `json:"azp"`
		Audience        audience       `json:"aud"`
		IssuedAt        int64          `json:"iat"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return fmt.Errorf("telegram ID token claims are invalid: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(transaction.Nonce)) != 1 {
		return errors.New("telegram ID token nonce mismatch")
	}
	if len(claims.Audience) > 1 && claims.AuthorizedParty != auth.oauth.ClientID {
		return errors.New("telegram ID token authorized party mismatch")
	}
	issuedAt := time.Unix(claims.IssuedAt, 0)
	if claims.IssuedAt <= 0 || issuedAt.After(auth.now().UTC().Add(maximumOIDCClockSkew)) {
		return errors.New("telegram ID token issue time is invalid")
	}
	if strings.TrimSpace(claims.Subject) == "" || len(claims.Subject) > 255 {
		return errors.New("telegram ID token subject is invalid")
	}
	if claims.TelegramUserID <= 0 {
		return errors.New("telegram ID token has no positive user ID claim")
	}
	sessionToken, err := auth.opaqueCredentialWithBytes(48)
	if err != nil {
		return err
	}
	now := auth.now().UTC()
	session := WebSession{
		TokenHash: sha256.Sum256([]byte(sessionToken)), TelegramUserID: int64(claims.TelegramUserID),
		OIDCIssuer: telegramIssuer, OIDCSubject: claims.Subject,
		Language: transaction.Language, CreatedAt: now, ExpiresAt: now.Add(auth.session),
	}
	if err := auth.store.CreateWebSession(request.Context(), session); err != nil {
		return fmt.Errorf("create web session: %w", err)
	}
	http.SetCookie(w, hostCookie(sessionCookie, sessionToken, session.ExpiresAt, true))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// Remove the authorization code and state from browser history immediately.
	http.Redirect(w, request, auth.publicOrigin+localizedAccountPath(transaction.Language), http.StatusSeeOther)
	return nil
}

func (auth *OIDCAuthenticator) Authenticate(request *http.Request) (WebSession, string, error) {
	cookie, err := request.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return WebSession{}, "", ErrUnauthenticated
	}
	session, err := auth.store.WebSession(request.Context(), sha256.Sum256([]byte(cookie.Value)), auth.now().UTC())
	if err != nil {
		return WebSession{}, "", ErrUnauthenticated
	}
	if session.OIDCIssuer != telegramIssuer || strings.TrimSpace(session.OIDCSubject) == "" || session.TelegramUserID <= 0 {
		return WebSession{}, "", ErrUnauthenticated
	}
	return session, auth.csrfToken(cookie.Value), nil
}

func (auth *OIDCAuthenticator) RequireCSRF(request *http.Request) (WebSession, error) {
	session, expected, err := auth.Authenticate(request)
	if err != nil {
		return WebSession{}, err
	}
	if request.Header.Get("Origin") != auth.publicOrigin {
		return WebSession{}, fmt.Errorf("%w: origin", errInvalidCSRFRequest)
	}
	provided := request.Header.Get("X-Astrosferum-CSRF")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return WebSession{}, fmt.Errorf("%w: token", errInvalidCSRFRequest)
	}
	return session, nil
}

// UpdateLanguage persists the explicit site-language choice for the current
// Telegram identity and all of its active web sessions.
func (auth *OIDCAuthenticator) UpdateLanguage(request *http.Request, language string) error {
	if language != "ru" && language != "en" {
		return errors.New("unsupported web language")
	}
	session, err := auth.RequireCSRF(request)
	if err != nil {
		return err
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return ErrUnauthenticated
	}
	return auth.store.UpdateWebSessionLanguage(
		request.Context(),
		sha256.Sum256([]byte(cookie.Value)),
		session.TelegramUserID,
		language,
	)
}

func (auth *OIDCAuthenticator) Logout(w http.ResponseWriter, request *http.Request) error {
	if _, err := auth.RequireCSRF(request); err != nil {
		return err
	}
	cookie, _ := request.Cookie(sessionCookie)
	if cookie != nil && cookie.Value != "" {
		if err := auth.store.RevokeWebSession(request.Context(), sha256.Sum256([]byte(cookie.Value))); err != nil {
			return fmt.Errorf("revoke web session: %w", err)
		}
	}
	clearHostCookie(w, sessionCookie)
	w.Header().Set("Cache-Control", "no-store")
	return nil
}

func (auth *OIDCAuthenticator) csrfToken(sessionToken string) string {
	digest := hmac.New(sha256.New, auth.csrfKey)
	_, _ = digest.Write([]byte(sessionToken))
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

func (auth *OIDCAuthenticator) opaqueCredential() (string, error) {
	return auth.opaqueCredentialWithBytes(minimumOpaqueCredentialBytes)
}

func (auth *OIDCAuthenticator) opaqueCredentialWithBytes(size int) (string, error) {
	buffer := make([]byte, size)
	if err := auth.random(buffer); err != nil {
		return "", fmt.Errorf("generate random credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hostCookie(name, value string, expires time.Time, httpOnly bool) *http.Cookie {
	return &http.Cookie{
		Name: name, Value: value, Path: "/", Expires: expires,
		Secure: true, HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode,
	}
}

func clearHostCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", Expires: time.Unix(1, 0), MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func preferredLanguage(request *http.Request) string {
	if requested := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("lang"))); requested == "ru" || requested == "en" {
		return requested
	}
	item := strings.SplitN(request.Header.Get("Accept-Language"), ",", 2)[0]
	language := strings.ToLower(strings.TrimSpace(strings.SplitN(item, ";", 2)[0]))
	if language == "ru" || strings.HasPrefix(language, "ru-") {
		return "ru"
	}
	return "en"
}

func localizedAccountPath(language string) string {
	if language == "ru" {
		return "/ru/account/sky-conditions"
	}
	return "/en/account/sky-conditions"
}

// audience accepts the OIDC aud claim in either its scalar or array form.
type audience []string

// telegramUserID accepts the integer form documented by Telegram and the
// decimal-string form currently emitted by some OIDC deployments. Both forms
// are normalized to the same positive int64 identity; fractions, exponents,
// signs, whitespace, and non-canonical strings remain invalid.
type telegramUserID int64

func (value *telegramUserID) UnmarshalJSON(data []byte) error {
	encoded := string(data)
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		encoded = text
	}
	parsed, err := strconv.ParseInt(encoded, 10, 64)
	if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != encoded {
		return errors.New("telegram user ID must be a canonical positive integer")
	}
	*value = telegramUserID(parsed)
	return nil
}

func (value *audience) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*value = list
		return nil
	}
	var scalar string
	if err := json.Unmarshal(data, &scalar); err != nil {
		return err
	}
	*value = []string{scalar}
	return nil
}
