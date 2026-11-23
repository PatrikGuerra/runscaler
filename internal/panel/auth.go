package panel

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"
)

const (
	pbkdf2Iterations  = 600_000
	minPasswordLength = 12
	sessionTTL        = 12 * time.Hour
	maxLoginFailures  = 5
	loginLockout      = 15 * time.Minute
	sessionCookie     = "panel_session"
	csrfHeader        = "X-CSRF-Token"
)

var (
	errBadPassword     = errors.New("invalid password")
	errLocked          = errors.New("too many failed logins; try again later")
	errBadSetupToken   = errors.New("invalid setup token")
	errPasswordTooWeak = errors.New("password must have at least 12 characters")
)

// Auth handles the single admin account: one-time setup, login with a
// lockout, sessions and CSRF tokens.
type Auth struct {
	store      *Store
	setupToken string // empty once an admin exists

	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
	now         func() time.Time
}

// NewAuth returns the setup token to print when no admin exists yet.
func NewAuth(ctx context.Context, store *Store) (*Auth, string, error) {
	a := &Auth{store: store, now: time.Now}
	_, exists, err := store.Admin(ctx)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		a.setupToken = randomToken(24)
	}
	return a, a.setupToken, nil
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashPassword(password string, salt []byte, iterations int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, iterations, 32)
}

func (a *Auth) SetupRequired(ctx context.Context) (bool, error) {
	_, exists, err := a.store.Admin(ctx)
	return !exists, err
}

// Setup creates the admin once, then returns a session token.
func (a *Auth) Setup(ctx context.Context, setupToken, password string) (string, error) {
	a.mu.Lock()
	expected := a.setupToken
	a.mu.Unlock()
	if expected == "" || subtle.ConstantTimeCompare([]byte(setupToken), []byte(expected)) != 1 {
		return "", errBadSetupToken
	}
	if len(password) < minPasswordLength {
		return "", errPasswordTooWeak
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	hash, err := hashPassword(password, salt, pbkdf2Iterations)
	if err != nil {
		return "", err
	}
	if err := a.store.CreateAdmin(ctx, AdminRecord{Hash: hash, Salt: salt, Iterations: pbkdf2Iterations}); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.setupToken = ""
	a.mu.Unlock()
	return a.newSession(ctx)
}

// Login checks the password and returns a session token.
func (a *Auth) Login(ctx context.Context, password string) (string, error) {
	a.mu.Lock()
	if a.now().Before(a.lockedUntil) {
		a.mu.Unlock()
		return "", errLocked
	}
	a.mu.Unlock()

	admin, exists, err := a.store.Admin(ctx)
	if err != nil {
		return "", err
	}
	ok := false
	if exists {
		hash, err := hashPassword(password, admin.Salt, admin.Iterations)
		if err != nil {
			return "", err
		}
		ok = subtle.ConstantTimeCompare(hash, admin.Hash) == 1
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !ok {
		a.failures++
		if a.failures >= maxLoginFailures {
			a.failures = 0
			a.lockedUntil = a.now().Add(loginLockout)
		}
		return "", errBadPassword
	}
	a.failures = 0
	return a.newSessionLocked(ctx)
}

func (a *Auth) newSession(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.newSessionLocked(ctx)
}

func (a *Auth) newSessionLocked(ctx context.Context) (string, error) {
	token := randomToken(32)
	if err := a.store.CreateSession(ctx, tokenHash(token), randomToken(24), sessionTTL); err != nil {
		return "", err
	}
	return token, nil
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// sessionFrom returns the CSRF token of the request's session.
func (a *Auth) sessionFrom(r *http.Request) (token, csrf string, ok bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", "", false
	}
	csrf, ok, err = a.store.Session(r.Context(), tokenHash(c.Value))
	if err != nil || !ok {
		return "", "", false
	}
	return c.Value, csrf, true
}

func (a *Auth) Logout(r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.store.DeleteSession(r.Context(), tokenHash(c.Value))
	}
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge: int(sessionTTL / time.Second), HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// requireSession rejects requests without a live session, and state-changing
// requests without the session's CSRF token.
func (a *Auth) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, csrf, ok := a.sessionFrom(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(csrf)) != 1 {
				writeError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
