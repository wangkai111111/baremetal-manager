package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
)

// credentialKey 从环境变量读取并校验 BMC 凭据加密密钥。
func credentialKey() ([]byte, error) {
	key := []byte(os.Getenv("BMC_ENCRYPTION_KEY"))
	if len(key) != 32 {
		return nil, errors.New("BMC_ENCRYPTION_KEY must be exactly 32 bytes")
	}
	return key, nil
}

// EncryptCredential 使用 AES-GCM 加密 BMC 凭据并返回带版本前缀的文本。
func EncryptCredential(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	key, err := credentialKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), nil)), nil
}

// DecryptCredential 解密带版本前缀的 BMC 凭据，拒绝未加密的旧凭据。
func DecryptCredential(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) < 3 || value[:3] != "v1:" {
		return "", errors.New("unencrypted BMC credential: migrate legacy credentials before use")
	}
	key, err := credentialKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	data, err := base64.RawStdEncoding.DecodeString(value[3:])
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
