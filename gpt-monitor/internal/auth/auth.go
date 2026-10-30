package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const CookieName = "gpt_monitor_session"

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("login rate limited")
)

type Config struct {
	Username      string
	PasswordHash  string
	SessionSecret string
	SessionTTL    time.Duration
	CookieSecure  bool
	MaxAttempts   int
	AttemptWindow time.Duration
	Now           func() time.Time
}

type Manager struct {
	config   Config
	mu       sync.Mutex
	sessions map[string]time.Time
	attempts map[string]attemptState
}

type attemptState struct {
	Count       int
	WindowStart time.Time
}

type Session struct {
	Authenticated bool      `json:"authenticated"`
	Username      string    `json:"username,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

func NewManager(config Config) (*Manager, error) {
	if strings.TrimSpace(config.Username) == "" {
		return nil, fmt.Errorf("admin username is required")
	}
	if strings.TrimSpace(config.PasswordHash) == "" {
		return nil, fmt.Errorf("admin password hash is required")
	}
	if len(config.SessionSecret) < 32 {
		return nil, fmt.Errorf("session secret must contain at least 32 characters")
	}
	if config.SessionTTL <= 0 {
		return nil, fmt.Errorf("session TTL must be positive")
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 5
	}
	if config.AttemptWindow <= 0 {
		config.AttemptWindow = 15 * time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Manager{
		config:   config,
		sessions: make(map[string]time.Time),
		attempts: make(map[string]attemptState),
	}, nil
}

// Login validates credentials and creates an opaque server-side session.
func (m *Manager) Login(request *http.Request, username, password string) (Session, *http.Cookie, error) {
	ip := clientIP(request)
	if m.rateLimited(ip) {
		return Session{}, nil, ErrRateLimited
	}

	validUsername := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(username)), []byte(m.config.Username)) == 1
	passwordErr := bcrypt.CompareHashAndPassword([]byte(m.config.PasswordHash), []byte(password))
	if !validUsername || passwordErr != nil {
		m.recordFailure(ip)
		return Session{}, nil, ErrInvalidCredentials
	}

	m.clearFailures(ip)
	now := m.config.Now().UTC()
	expiresAt := now.Add(m.config.SessionTTL)
	token, err := randomToken(32)
	if err != nil {
		return Session{}, nil, fmt.Errorf("create session token: %w", err)
	}
	signed := m.sign(token)

	m.mu.Lock()
	m.sessions[token] = expiresAt
	m.pruneLocked(now)
	m.mu.Unlock()

	session := Session{
		Authenticated: true,
		Username:      m.config.Username,
		ExpiresAt:     expiresAt,
	}
	return session, &http.Cookie{
		Name:     CookieName,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(m.config.SessionTTL.Seconds()),
	}, nil
}

func (m *Manager) Authenticate(request *http.Request) (Session, bool) {
	cookie, err := request.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return Session{}, false
	}
	token, ok := m.verify(cookie.Value)
	if !ok {
		return Session{}, false
	}

	now := m.config.Now().UTC()
	m.mu.Lock()
	expiresAt, exists := m.sessions[token]
	if !exists || !now.Before(expiresAt) {
		delete(m.sessions, token)
		m.mu.Unlock()
		return Session{}, false
	}
	m.mu.Unlock()
	return Session{Authenticated: true, Username: m.config.Username, ExpiresAt: expiresAt}, true
}

func (m *Manager) Logout(w http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie(CookieName); err == nil {
		if token, ok := m.verify(cookie.Value); ok {
			m.mu.Lock()
			delete(m.sessions, token)
			m.mu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func (m *Manager) sign(token string) string {
	mac := hmac.New(sha256.New, []byte(m.config.SessionSecret))
	_, _ = mac.Write([]byte(token))
	signature := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(token)) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (m *Manager) verify(value string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(m.config.SessionSecret))
	_, _ = mac.Write(tokenBytes)
	expected := mac.Sum(nil)
	if !hmac.Equal(signature, expected) {
		return "", false
	}
	return string(tokenBytes), true
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (m *Manager) rateLimited(ip string) bool {
	now := m.config.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.attempts[ip]
	if !ok {
		return false
	}
	if now.Sub(state.WindowStart) >= m.config.AttemptWindow {
		delete(m.attempts, ip)
		return false
	}
	return state.Count >= m.config.MaxAttempts
}

func (m *Manager) recordFailure(ip string) {
	now := m.config.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.attempts[ip]
	if state.WindowStart.IsZero() || now.Sub(state.WindowStart) >= m.config.AttemptWindow {
		state = attemptState{Count: 0, WindowStart: now}
	}
	state.Count++
	m.attempts[ip] = state
}

func (m *Manager) clearFailures(ip string) {
	m.mu.Lock()
	delete(m.attempts, ip)
	m.mu.Unlock()
}

func (m *Manager) pruneLocked(now time.Time) {
	for token, expiresAt := range m.sessions {
		if !now.Before(expiresAt) {
			delete(m.sessions, token)
		}
	}
}

func clientIP(request *http.Request) string {
	if request == nil {
		return "unknown"
	}
	if forwarded := strings.TrimSpace(request.Header.Get("X-Forwarded-For")); forwarded != "" {
		first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
		if first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if request.RemoteAddr != "" {
		return request.RemoteAddr
	}
	return "unknown"
}
