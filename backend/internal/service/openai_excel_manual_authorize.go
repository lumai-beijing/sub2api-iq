package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxExcelManualAuthorizeCallbackURL = 2048

// Narrow dependencies keep the manual flow testable without a live database or
// the automatic re-login worker.
type excelManualAuthorizeAccountReader interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
}

type excelManualAuthorizeProxyReader interface {
	GetByID(ctx context.Context, id int64) (*Proxy, error)
}

// excelManualAuthorizeGrantStore persists the independent Excel grant and
// clears the preparation marker in one transaction. It deliberately does not
// depend on openai_oauth_reauth_tasks: manual authorization has no worker task.
type excelManualAuthorizeGrantStore interface {
	ApplyExcelManualGrant(ctx context.Context, accountID int64, ciphertext string, state ExcelBPSCredentialState) error
}

type excelManualAuthorizeService struct {
	oauth     *OpenAIOAuthService
	encryptor SecretEncryptor
	accounts  excelManualAuthorizeAccountReader
	proxies   excelManualAuthorizeProxyReader
	db        excelManualAuthorizeGrantStore
}

// newExcelManualAuthorizeService assembles the manual flow from the same
// dependencies the automatic Excel re-authorization uses.
func (s *adminServiceImpl) newExcelManualAuthorizeService() (*excelManualAuthorizeService, error) {
	if s == nil || s.excelOAuthReauth == nil || s.excelOAuthReauth.oauth == nil ||
		s.excelOAuthReauth.encryptor == nil || s.accountRepo == nil || s.proxyRepo == nil || s.entClient == nil {
		return nil, infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_UNAVAILABLE",
			"Excel manual authorization is not available",
		)
	}
	return &excelManualAuthorizeService{
		oauth:     s.excelOAuthReauth.oauth,
		encryptor: s.excelOAuthReauth.encryptor,
		accounts:  s.accountRepo,
		proxies:   s.proxyRepo,
		db:        &excelManualAuthorizeEntStore{client: s.entClient},
	}, nil
}

// BeginExcelManualAuthorize returns an official Excel OAuth URL whose callback
// is pasted back by an administrator after a browser login.
func (s *adminServiceImpl) BeginExcelManualAuthorize(ctx context.Context, accountID int64) (string, string, error) {
	svc, err := s.newExcelManualAuthorizeService()
	if err != nil {
		return "", "", err
	}
	return svc.Begin(ctx, accountID)
}

// CompleteExcelManualAuthorize exchanges the pasted callback for an Excel grant
// and stores it under the existing account. It never touches the BPS switch.
func (s *adminServiceImpl) CompleteExcelManualAuthorize(ctx context.Context, accountID int64, sessionID, callbackURL string) error {
	svc, err := s.newExcelManualAuthorizeService()
	if err != nil {
		return err
	}
	return svc.Complete(ctx, accountID, sessionID, callbackURL)
}

func (s *excelManualAuthorizeService) Begin(ctx context.Context, accountID int64) (string, string, error) {
	account, err := s.eligibleAccount(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	proxyURL, err := s.proxyURL(ctx, account.ProxyID)
	if err != nil {
		return "", "", err
	}
	result, err := s.oauth.GenerateAuthURLWithProxyURL(ctx, proxyURL, "", PlatformOpenAI, "excel")
	if err != nil {
		return "", "", err
	}
	if result == nil || strings.TrimSpace(result.SessionID) == "" || strings.TrimSpace(result.AuthURL) == "" {
		return "", "", infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_SESSION_FAILED",
			"Failed to create an Excel authorization session",
		)
	}
	return result.SessionID, result.AuthURL, nil
}

func (s *excelManualAuthorizeService) Complete(ctx context.Context, accountID int64, sessionID, callbackURL string) error {
	account, err := s.eligibleAccount(ctx, accountID)
	if err != nil {
		return err
	}
	code, state, err := parseExcelManualAuthorizeCallback(callbackURL)
	if err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return infraerrors.BadRequest("EXCEL_MANUAL_AUTHORIZE_SESSION_REQUIRED", "a session id is required")
	}
	info, err := s.oauth.ExchangeCode(ctx, &OpenAIExchangeCodeInput{
		SessionID: sessionID,
		Code:      code,
		State:     state,
		ProxyID:   account.ProxyID,
	})
	if err != nil {
		return err
	}
	if info == nil {
		return infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_EXCHANGE_FAILED",
			"Excel authorization did not return a token",
		)
	}
	// Same guard as the automatic worker: a pasted callback may belong to a
	// different ChatGPT workspace/user, so refuse before writing anything.
	if err := validateReauthToken(account, info); err != nil {
		return err
	}
	raw, err := json.Marshal(s.oauth.BuildAccountCredentials(info))
	if err != nil {
		return infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_CREDENTIALS_FAILED",
			"Failed to encode Excel credentials",
		)
	}
	ciphertext, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_CREDENTIALS_FAILED",
			"Failed to encrypt Excel credentials",
		)
	}
	if s.db == nil {
		return infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_UNAVAILABLE",
			"Excel credential storage is unavailable",
		)
	}
	return s.db.ApplyExcelManualGrant(ctx, account.ID, ciphertext, ExcelBPSGrantState(info.ExpiresAt))
}

func (s *excelManualAuthorizeService) eligibleAccount(ctx context.Context, accountID int64) (*Account, error) {
	if accountID <= 0 {
		return nil, infraerrors.BadRequest("EXCEL_MANUAL_AUTHORIZE_ACCOUNT_INVALID", "invalid account id")
	}
	if s == nil || s.accounts == nil {
		return nil, infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_UNAVAILABLE",
			"Excel manual authorization is not available",
		)
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return nil, infraerrors.New(http.StatusNotFound, "EXCEL_MANUAL_AUTHORIZE_ACCOUNT_NOT_FOUND", "account not found")
	}
	// Excel-only accounts cannot host a second Excel grant, and the remaining
	// checks keep this endpoint aligned with the quality/BPS eligibility rules.
	if !account.IsOAuth() || account.Platform != PlatformOpenAI || account.IsExcelOAuth() || !QualityBPSEligible(account) {
		return nil, infraerrors.BadRequest(
			"EXCEL_MANUAL_AUTHORIZE_ACCOUNT_INVALID",
			"only an OpenAI OAuth account that is not Excel-only can be manually authorized",
		)
	}
	return account, nil
}

func (s *excelManualAuthorizeService) proxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if *proxyID <= 0 {
		return "", infraerrors.BadRequest("EXCEL_MANUAL_AUTHORIZE_PROXY_UNAVAILABLE", "proxy is unavailable")
	}
	if s == nil || s.proxies == nil {
		return "", infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_UNAVAILABLE",
			"Excel manual authorization is not available",
		)
	}
	proxy, err := s.proxies.GetByID(ctx, *proxyID)
	if err != nil || proxy == nil || !proxy.IsActive() || proxy.IsExpired(time.Now()) || strings.TrimSpace(proxy.URL()) == "" {
		return "", infraerrors.BadRequest("EXCEL_MANUAL_AUTHORIZE_PROXY_UNAVAILABLE", "proxy is unavailable")
	}
	return proxy.URL(), nil
}

func parseExcelManualAuthorizeCallback(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxExcelManualAuthorizeCallbackURL {
		return "", "", infraerrors.BadRequest(
			"EXCEL_MANUAL_AUTHORIZE_CALLBACK_INVALID",
			"callback URL is invalid",
		)
	}
	parsed, err := url.Parse(raw)
	// Only the official Excel redirect host is accepted, so a callback copied
	// from another OAuth flow is rejected before the session is consumed.
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "bps.openai.com") {
		return "", "", infraerrors.BadRequest(
			"EXCEL_MANUAL_AUTHORIZE_CALLBACK_INVALID",
			"callback URL is invalid",
		)
	}
	code := strings.TrimSpace(parsed.Query().Get("code"))
	state := strings.TrimSpace(parsed.Query().Get("state"))
	if code == "" || state == "" {
		return "", "", infraerrors.BadRequest(
			"EXCEL_MANUAL_AUTHORIZE_CALLBACK_INVALID",
			"callback URL must contain code and state",
		)
	}
	return code, state, nil
}

// excelManualAuthorizeEntStore writes the grant with the same lock/write order
// as the automatic ApplyExcelCredentials path, but keyed only by account id.
type excelManualAuthorizeEntStore struct {
	client *dbent.Client
}

func (s *excelManualAuthorizeEntStore) ApplyExcelManualGrant(ctx context.Context, accountID int64, ciphertext string, state ExcelBPSCredentialState) error {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if s == nil || s.client == nil {
		return infraerrors.New(
			http.StatusInternalServerError,
			"EXCEL_MANUAL_AUTHORIZE_UNAVAILABLE",
			"Excel credential storage is unavailable",
		)
	}
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	rows, err := tx.Client().QueryContext(ctx,
		`SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID)
	if err != nil {
		return err
	}
	if !rows.Next() {
		nextErr := rows.Err()
		_ = rows.Close()
		if nextErr != nil {
			return nextErr
		}
		return infraerrors.Conflict(
			"EXCEL_MANUAL_AUTHORIZE_ACCOUNT_CHANGED",
			"account was deleted while Excel authorization was running",
		)
	}
	if err = rows.Scan(&id); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `
INSERT INTO openai_excel_oauth_credentials(account_id,credentials_ciphertext,updated_at)
VALUES($1,$2,NOW())
ON CONFLICT(account_id) DO UPDATE
SET credentials_ciphertext=EXCLUDED.credentials_ciphertext,updated_at=NOW()`, id, ciphertext); err != nil {
		return err
	}
	// Clear only the preparation marker and record the same safe grant metadata
	// as the automatic path. The BPS switch and every other extra key stay
	// untouched; enabling BPS remains an explicit admin/quality choice.
	if _, err = tx.Client().ExecContext(ctx,
		`UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb)-'openai_excel_bps_authorization_pending',
 ARRAY['openai_excel_bps_credential_state'],$2::jsonb,true),updated_at=NOW() WHERE id=$1`,
		id, string(stateJSON)); err != nil {
		return err
	}
	return tx.Commit()
}
