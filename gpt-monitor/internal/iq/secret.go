package iq

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Sealer 把第三方供应商的 API Key 加密后再落库：SQLite 文件或它的备份被拷走时 Key 不至于明文泄露。
// 密钥由 .env 里的 IQ_SECRET_KEY（没配时用 SESSION_SECRET）派生，换密钥后旧 Key 解不开，需要在界面上重填。
type Sealer struct {
	aead cipher.AEAD
}

const sealPrefix = "v1:"

func NewSealer(secret string) (*Sealer, error) {
	if len(strings.TrimSpace(secret)) < 16 {
		return nil, errors.New("iq secret must contain at least 16 characters")
	}
	key := sha256.Sum256([]byte("gpt-pro-monitor/iq-provider-key/v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal 用随机 nonce 加密；同一个明文每次密文都不同。
func (s *Sealer) Seal(plain string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("seal: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plain), nil)
	return sealPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *Sealer) Open(sealed string) (string, error) {
	raw, ok := strings.CutPrefix(sealed, sealPrefix)
	if !ok {
		return "", errors.New("unknown key format")
	}
	data, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil || len(data) < s.aead.NonceSize() {
		return "", errors.New("corrupted key")
	}
	plain, err := s.aead.Open(nil, data[:s.aead.NonceSize()], data[s.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("无法解密 API Key（监控的密钥换过？请在界面上重新填写）")
	}
	return string(plain), nil
}

// keyHint 只露末 4 位，给界面上辨认是哪把 Key 用。
func keyHint(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}
