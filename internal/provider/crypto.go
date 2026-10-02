package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

func Encrypt(masterKey []byte, plaintext string) ([]byte, error) {
	gcm, err := newGCM(masterKey)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func Decrypt(masterKey, enc []byte) (string, error) {
	gcm, err := newGCM(masterKey)
	if err != nil {
		return "", err
	}
	if len(enc) < gcm.NonceSize() {
		return "", errors.New("api_key_enc 格式无效")
	}
	nonce, ciphertext := enc[:gcm.NonceSize()], enc[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("api_key 解密失败")
	}
	return string(plain), nil
}

func newGCM(masterKey []byte) (cipher.AEAD, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("master key 必须是 32 字节")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func MaskKey(key string) string {
	if len(key) >= 8 {
		return key[:3] + "****" + key[len(key)-4:]
	}
	return "****"
}
