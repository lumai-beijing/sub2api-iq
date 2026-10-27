package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type excelManualAuthorizeAccountReaderStub struct {
	account *Account
	err     error
}

func (r *excelManualAuthorizeAccountReaderStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	copy := *r.account
	copy.Credentials = cloneReauthMap(r.account.Credentials)
	copy.Extra = cloneReauthMap(r.account.Extra)
	return &copy, nil
}

type excelManualAuthorizeProxyReaderStub struct {
	proxy *Proxy
	err   error
}

func (r *excelManualAuthorizeProxyReaderStub) GetByID(_ context.Context, _ int64) (*Proxy, error) {
	return r.proxy, r.err
}

type excelManualAuthorizeGrantStoreStub struct {
	pending    map[int64]bool
	grants     map[int64]string
	ciphertext string
	state      ExcelBPSCredentialState
	calls      int
	err        error
}

func (s *excelManualAuthorizeGrantStoreStub) ApplyExcelManualGrant(_ context.Context, accountID int64, ciphertext string, state ExcelBPSCredentialState) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	s.ciphertext = ciphertext
	s.state = state
	s.grants[accountID] = ciphertext
	delete(s.pending, accountID)
	return nil
}

func newExcelManualAuthorizeTestService(t *testing.T, mutate func(*Account)) (*excelManualAuthorizeService, *reauthTestOAuthClient, *excelManualAuthorizeGrantStoreStub) {
	t.Helper()
	account := &Account{
		ID:       42,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "old-access",
			"refresh_token":      "old-refresh",
			"chatgpt_account_id": "acct-1",
			"chatgpt_user_id":    "user-1",
			"email":              "user@example.com",
			"plan_type":          "pro",
		},
		Extra: map[string]any{
			"openai_excel_bps":                       true,
			ExcelBPSAuthorizationPendingKey:          true,
			"unrelated_configuration_that_must_stay": "keep-me",
		},
	}
	if mutate != nil {
		mutate(account)
	}
	reader := &excelManualAuthorizeAccountReaderStub{account: account}
	oauthClient := &reauthTestOAuthClient{accountID: "acct-1", userID: "user-1", email: "user@example.com"}
	oauthService := NewOpenAIOAuthService(nil, oauthClient)
	t.Cleanup(oauthService.Stop)
	store := &excelManualAuthorizeGrantStoreStub{
		pending: map[int64]bool{account.ID: true},
		grants:  make(map[int64]string),
	}
	return &excelManualAuthorizeService{
		oauth:     oauthService,
		encryptor: reauthTestEncryptor{},
		accounts:  reader,
		proxies:   &excelManualAuthorizeProxyReaderStub{},
		db:        store,
	}, oauthClient, store
}

func beginExcelManualAuthorizeForTest(t *testing.T, svc *excelManualAuthorizeService) (string, string) {
	t.Helper()
	sessionID, authURL, err := svc.Begin(context.Background(), 42)
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)
	require.NotEmpty(t, authURL)
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, openai.ExcelClientID, parsed.Query().Get("client_id"))
	require.NotEmpty(t, parsed.Query().Get("state"))
	return sessionID, parsed.Query().Get("state")
}

func TestExcelManualAuthorizeRejectsNonOAuthAccount(t *testing.T) {
	svc, _, store := newExcelManualAuthorizeTestService(t, func(account *Account) {
		account.Type = AccountTypeAPIKey
	})
	_, _, err := svc.Begin(context.Background(), 42)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Zero(t, store.calls)
}

func TestExcelManualAuthorizeRejectsExcelOnlyAccount(t *testing.T) {
	svc, _, store := newExcelManualAuthorizeTestService(t, func(account *Account) {
		account.Credentials["client_id"] = openai.ExcelClientID
	})
	_, _, err := svc.Begin(context.Background(), 42)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Zero(t, store.calls)
}

func TestExcelManualAuthorizeRejectsCallbackWithoutCodeOrState(t *testing.T) {
	svc, _, store := newExcelManualAuthorizeTestService(t, nil)
	for _, callback := range []string{
		"https://bps.openai.com/basispoints/api/office/auth/callback?state=only-state",
		"https://bps.openai.com/basispoints/api/office/auth/callback?code=only-code",
		"not a url",
		// Callbacks from other OAuth flows (Codex localhost, look-alike hosts) are refused.
		"http://localhost:1455/auth/callback?code=code-1&state=s",
		"https://bps.openai.com.evil.example/auth/callback?code=code-1&state=s",
		"http://bps.openai.com/auth/callback?code=code-1&state=s",
	} {
		err := svc.Complete(context.Background(), 42, "session-id", callback)
		require.Error(t, err, callback)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err), callback)
	}
	require.Zero(t, store.calls)
}

func TestExcelManualAuthorizeRejectsIdentityMismatch(t *testing.T) {
	svc, oauthClient, store := newExcelManualAuthorizeTestService(t, nil)
	sessionID, state := beginExcelManualAuthorizeForTest(t, svc)
	oauthClient.accountID = "acct-2"

	callback := "https://bps.openai.com/basispoints/api/office/auth/callback?code=code-1&state=" + url.QueryEscape(state)
	err := svc.Complete(context.Background(), 42, sessionID, callback)
	require.Error(t, err)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Zero(t, store.calls)
}

func TestExcelManualAuthorizeStoresGrantAndClearsPending(t *testing.T) {
	svc, _, store := newExcelManualAuthorizeTestService(t, nil)
	sessionID, state := beginExcelManualAuthorizeForTest(t, svc)

	callback := "https://bps.openai.com/basispoints/api/office/auth/callback?code=code-1&state=" + url.QueryEscape(state)
	require.NoError(t, svc.Complete(context.Background(), 42, sessionID, callback))

	require.Equal(t, 1, store.calls)
	require.NotEmpty(t, store.grants[42])
	require.False(t, store.pending[42], "pending authorization marker must be cleared")
	require.NotEqual(t, "", store.state.Status, "grant metadata must be recorded like the automatic path")

	raw, ok := strings.CutPrefix(store.ciphertext, "encrypted:")
	require.True(t, ok)
	var credentials map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &credentials))
	require.Equal(t, "new-access", credentials["access_token"])
	require.Equal(t, "new-refresh", credentials["refresh_token"])
	require.Equal(t, openai.ExcelClientID, credentials["client_id"])
	require.Equal(t, "acct-1", credentials["chatgpt_account_id"])
}

func TestExcelManualAuthorizeUnavailableWithoutDependencies(t *testing.T) {
	_, _, err := (&adminServiceImpl{}).BeginExcelManualAuthorize(context.Background(), 42)
	require.Error(t, err)
	require.Equal(t, http.StatusInternalServerError, infraerrors.Code(err))
}
