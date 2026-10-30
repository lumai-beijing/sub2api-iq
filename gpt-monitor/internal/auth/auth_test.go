package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLoginAuthenticateAndLogout(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		Username:      "admin",
		PasswordHash:  string(hash),
		SessionSecret: "01234567890123456789012345678901",
		SessionTTL:    time.Hour,
		CookieSecure:  true,
		MaxAttempts:   2,
		AttemptWindow: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	loginRequest := httptest.NewRequest(http.MethodPost, "https://monitor.test/api/login", nil)
	session, cookie, err := manager.Login(loginRequest, "admin", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if !session.Authenticated || cookie == nil || !cookie.Secure || !cookie.HttpOnly {
		t.Fatalf("unexpected login result: session=%+v cookie=%+v", session, cookie)
	}

	authRequest := httptest.NewRequest(http.MethodGet, "https://monitor.test/api/session", nil)
	authRequest.AddCookie(cookie)
	if _, ok := manager.Authenticate(authRequest); !ok {
		t.Fatal("session cookie should authenticate")
	}

	recorder := httptest.NewRecorder()
	manager.Logout(recorder, authRequest)
	if len(recorder.Result().Cookies()) == 0 || recorder.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout should expire cookie")
	}
	if _, ok := manager.Authenticate(authRequest); ok {
		t.Fatal("logged out session must no longer authenticate")
	}
}

func TestLoginRateLimit(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		Username:      "admin",
		PasswordHash:  string(hash),
		SessionSecret: "01234567890123456789012345678901",
		SessionTTL:    time.Hour,
		MaxAttempts:   2,
		AttemptWindow: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://monitor.test/api/login", nil)
	request.RemoteAddr = "10.0.0.1:1234"
	for i := 0; i < 2; i++ {
		if _, _, err := manager.Login(request, "admin", "wrong"); err != ErrInvalidCredentials {
			t.Fatalf("attempt %d error = %v", i+1, err)
		}
	}
	if _, _, err := manager.Login(request, "admin", "wrong"); err != ErrRateLimited {
		t.Fatalf("third attempt error = %v, want rate limit", err)
	}
}
