package astroweb

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type memoryAuthStore struct {
	mu                sync.Mutex
	transactions      map[[sha256.Size]byte]AuthTransaction
	sessions          map[[sha256.Size]byte]WebSession
	updateLanguageErr error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func newMemoryAuthStore() *memoryAuthStore {
	return &memoryAuthStore{
		transactions: make(map[[sha256.Size]byte]AuthTransaction),
		sessions:     make(map[[sha256.Size]byte]WebSession),
	}
}

func (store *memoryAuthStore) CreateAuthTransaction(_ context.Context, transaction AuthTransaction) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.transactions[transaction.HandleHash] = transaction
	return nil
}

func (store *memoryAuthStore) ConsumeAuthTransaction(_ context.Context, handle, state [sha256.Size]byte, now time.Time) (AuthTransaction, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	transaction, ok := store.transactions[handle]
	if !ok || transaction.StateHash != state || !transaction.ExpiresAt.After(now) {
		return AuthTransaction{}, ErrAuthTransactionStale
	}
	delete(store.transactions, handle)
	return transaction, nil
}

func (store *memoryAuthStore) CreateWebSession(_ context.Context, session WebSession) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.sessions[session.TokenHash] = session
	return nil
}

func (store *memoryAuthStore) WebSession(_ context.Context, token [sha256.Size]byte, now time.Time) (WebSession, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	session, ok := store.sessions[token]
	if !ok || !session.ExpiresAt.After(now) {
		return WebSession{}, ErrUnauthenticated
	}
	return session, nil
}

func (store *memoryAuthStore) RevokeWebSession(_ context.Context, token [sha256.Size]byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.sessions, token)
	return nil
}

func (store *memoryAuthStore) UpdateWebSessionLanguage(
	_ context.Context,
	token [sha256.Size]byte,
	userID int64,
	language string,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.updateLanguageErr != nil {
		return store.updateLanguageErr
	}
	session, ok := store.sessions[token]
	if !ok || session.TelegramUserID != userID {
		return ErrUnauthenticated
	}
	for key, candidate := range store.sessions {
		if candidate.TelegramUserID == userID {
			candidate.Language = language
			store.sessions[key] = candidate
		}
	}
	return nil
}

func TestOIDCConfigValidation(t *testing.T) {
	valid := OIDCConfig{
		Issuer: telegramIssuer, ClientID: "12345", ClientSecret: "secret",
		RedirectURL:  "https://astrosferum.com/auth/telegram/callback",
		PublicOrigin: "https://astrosferum.com", TransactionLifetime: 10 * time.Minute,
		SessionLifetime: 24 * time.Hour, CSRFKey: make([]byte, 32),
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	checks := []func(*OIDCConfig){
		func(value *OIDCConfig) { value.Issuer = "http://oauth.telegram.org" },
		func(value *OIDCConfig) { value.ClientID = "bot" },
		func(value *OIDCConfig) { value.ClientSecret = "" },
		func(value *OIDCConfig) { value.RedirectURL = "https://elsewhere.example/callback" },
		func(value *OIDCConfig) { value.PublicOrigin = "http://astrosferum.com" },
		func(value *OIDCConfig) { value.CSRFKey = make([]byte, 31) },
	}
	for index, mutate := range checks {
		candidate := valid
		mutate(&candidate)
		if err := candidate.validate(); err == nil {
			t.Fatalf("invalid config case %d accepted", index)
		}
	}
}

func TestBeginBindsOpaqueCookieStateNonceAndPKCE(t *testing.T) {
	store := newMemoryAuthStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	counter := byte(1)
	auth := &OIDCAuthenticator{
		store: store,
		oauth: oauth2.Config{
			ClientID: "12345", RedirectURL: "https://astrosferum.com/auth/telegram/callback",
			Endpoint: oauth2.Endpoint{AuthURL: "https://oauth.telegram.org/auth"},
			Scopes:   []string{"openid", "profile"},
		},
		publicOrigin: "https://astrosferum.com", transaction: 10 * time.Minute,
		session: 24 * time.Hour, csrfKey: make([]byte, 32), now: func() time.Time { return now },
		random: func(buffer []byte) error {
			for index := range buffer {
				buffer[index] = counter
				counter++
			}
			return nil
		},
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://astrosferum.com/auth/telegram/login", nil)
	request.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	response := httptest.NewRecorder()
	if err := auth.Begin(response, request); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != oidcTransactionCookie || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" {
		t.Fatalf("unexpected transaction cookie: %#v", cookies)
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("state") == "" || location.Query().Get("nonce") == "" || location.Query().Get("code_challenge_method") != "S256" || location.Query().Get("code_challenge") == "" {
		t.Fatalf("authorization URL misses state/nonce/PKCE: %s", location)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.transactions) != 1 {
		t.Fatalf("transactions = %d", len(store.transactions))
	}
	for _, transaction := range store.transactions {
		if transaction.Language != "ru" || transaction.Nonce != location.Query().Get("nonce") || transaction.StateHash != sha256.Sum256([]byte(location.Query().Get("state"))) {
			t.Fatalf("stored transaction not bound to redirect: %+v", transaction)
		}
	}
}

func TestCallbackUsesPinnedHTTPClientAndTelegramTokenRequest(t *testing.T) {
	store := newMemoryAuthStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	handle, state := "opaque-handle", "opaque-state"
	transaction := AuthTransaction{
		HandleHash: sha256.Sum256([]byte(handle)), StateHash: sha256.Sum256([]byte(state)),
		PKCEVerifier: "server-side-pkce-verifier", Nonce: "nonce", Language: "en",
		ExpiresAt: now.Add(time.Minute),
	}
	if err := store.CreateAuthTransaction(context.Background(), transaction); err != nil {
		t.Fatal(err)
	}
	called := false
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		if request.URL.String() != "https://oauth.telegram.org/token" {
			t.Fatalf("token URL = %s", request.URL)
		}
		clientID, secret, ok := request.BasicAuth()
		if !ok || clientID != "12345" || secret != "secret" {
			t.Fatalf("token endpoint did not receive HTTP Basic client authentication")
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("client_id") != "12345" || request.Form.Get("code_verifier") != transaction.PKCEVerifier {
			t.Fatalf("token form = %v", request.Form)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK",
			Header:  http.Header{"Content-Type": []string{"application/json"}},
			Body:    io.NopCloser(strings.NewReader(`{"access_token":"access","token_type":"Bearer"}`)),
			Request: request,
		}, nil
	})}
	auth := &OIDCAuthenticator{
		store: store,
		oauth: oauth2.Config{
			ClientID: "12345", ClientSecret: "secret",
			Endpoint:    oauth2.Endpoint{TokenURL: "https://oauth.telegram.org/token", AuthStyle: oauth2.AuthStyleInHeader},
			RedirectURL: "https://astrosferum.com/auth/telegram/callback",
		},
		httpClient: client, now: func() time.Time { return now },
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://astrosferum.com/auth/telegram/callback?code=code&state="+state, nil)
	request.AddCookie(hostCookie(oidcTransactionCookie, handle, transaction.ExpiresAt, true))
	if err := auth.Callback(httptest.NewRecorder(), request); err == nil || !strings.Contains(err.Error(), "no ID token") {
		t.Fatalf("callback error = %v", err)
	}
	if !called {
		t.Fatal("configured OIDC HTTP client was not used for the token exchange")
	}
}

func TestSessionAuthenticationCSRFAndLogout(t *testing.T) {
	store := newMemoryAuthStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	auth := &OIDCAuthenticator{
		store: store, publicOrigin: "https://astrosferum.com", csrfKey: []byte(strings.Repeat("k", 32)),
		now: func() time.Time { return now },
	}
	token := "opaque-session-token"
	session := WebSession{TokenHash: sha256.Sum256([]byte(token)), TelegramUserID: 42, OIDCIssuer: telegramIssuer, OIDCSubject: "subject-42", Language: "en", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.CreateWebSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://astrosferum.com/api/v1/logout", nil)
	request.AddCookie(hostCookie(sessionCookie, token, session.ExpiresAt, true))
	request.Header.Set("Origin", "https://astrosferum.com")
	request.Header.Set("X-Astrosferum-CSRF", auth.csrfToken(token))
	got, err := auth.RequireCSRF(request)
	if err != nil || got.TelegramUserID != 42 {
		t.Fatalf("valid session rejected: %+v, %v", got, err)
	}
	badOrigin := request.Clone(request.Context())
	badOrigin.Header = request.Header.Clone()
	badOrigin.Header.Set("Origin", "https://attacker.example")
	if _, err := auth.RequireCSRF(badOrigin); err == nil {
		t.Fatal("cross-origin request accepted")
	}
	response := httptest.NewRecorder()
	if err := auth.Logout(response, request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.Authenticate(request); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session authentication error = %v", err)
	}
	deleted := response.Result().Cookies()
	if len(deleted) != 1 || deleted[0].Name != sessionCookie || deleted[0].MaxAge != -1 {
		t.Fatalf("session cookie not deleted: %#v", deleted)
	}
}

func TestPreferredLanguageRussianOnly(t *testing.T) {
	for _, test := range []struct {
		header string
		want   string
	}{
		{header: "ru-RU,ru;q=0.9,en;q=0.8", want: "ru"},
		{header: "en-US,en;q=0.9", want: "en"},
		{header: "de-DE,de;q=0.9,ru;q=0.1", want: "en"},
		{header: "", want: "en"},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://astrosferum.com/", nil)
		request.Header.Set("Accept-Language", test.header)
		if got := preferredLanguage(request); got != test.want {
			t.Fatalf("preferredLanguage(%q) = %q, want %q", test.header, got, test.want)
		}
	}
}

func TestPreferredLanguageExplicitRouteChoiceWins(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		query  string
		header string
		want   string
	}{
		{query: "ru", header: "en-US,en;q=0.9", want: "ru"},
		{query: "en", header: "ru-RU,ru;q=0.9", want: "en"},
		{query: "de", header: "ru-RU,ru;q=0.9", want: "ru"},
		{query: "", header: "en-US,en;q=0.9", want: "en"},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"https://astrosferum.com/auth/telegram/login?lang="+test.query, nil)
		request.Header.Set("Accept-Language", test.header)
		if got := preferredLanguage(request); got != test.want {
			t.Errorf("preferredLanguage(lang=%q, header=%q) = %q, want %q", test.query, test.header, got, test.want)
		}
	}
	if got := localizedAccountPath("ru"); got != "/ru/account/sky-conditions" {
		t.Fatalf("Russian account path = %q", got)
	}
	if got := localizedAccountPath("en"); got != "/en/account/sky-conditions" {
		t.Fatalf("English account path = %q", got)
	}
}

func TestTelegramUserIDAcceptsDocumentedIntegerAndDecimalString(t *testing.T) {
	for _, encoded := range []string{`621424272`, `"621424272"`} {
		var value telegramUserID
		if err := value.UnmarshalJSON([]byte(encoded)); err != nil || int64(value) != 621424272 {
			t.Fatalf("telegram user ID %s = %d, %v", encoded, value, err)
		}
	}
	for _, encoded := range []string{`0`, `-1`, `1.0`, `1e3`, `"0621424272"`, `"user"`, `null`} {
		var value telegramUserID
		if err := value.UnmarshalJSON([]byte(encoded)); err == nil {
			t.Fatalf("invalid Telegram user ID %s was accepted as %d", encoded, value)
		}
	}
}
