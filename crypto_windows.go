package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the app encrypts with Chromium's scheme: an AES-256 key stored in
// "Local State", protected by DPAPI for the current Windows user, and values
// prefixed "v10" followed by a 12-byte nonce, the ciphertext and a GCM tag.

var (
	keyOnce sync.Once
	keyVal  []byte
	keyErr  error
)

func appKey() ([]byte, error) {
	keyOnce.Do(func() { keyVal, keyErr = loadAppKey() })
	return keyVal, keyErr
}

func loadAppKey() ([]byte, error) {
	data, err := os.ReadFile(claudeDesktop().localStatePath())
	if err != nil {
		return nil, err
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	blob, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil || len(blob) < 6 || string(blob[:5]) != "DPAPI" {
		return nil, errors.New(tr("unexpected key format in Local State", "Local State 中的密钥格式不受支持"))
	}
	blob = blob[5:]
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func decryptValue(data []byte) ([]byte, error) {
	if len(data) < 3+12+16 || string(data[:3]) != "v10" {
		return nil, errors.New(tr("unexpected encrypted value format", "登录凭证的加密格式不受支持"))
	}
	key, err := appKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, data[3:15], data[15:], nil)
}
