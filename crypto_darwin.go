package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"errors"
	"os/exec"
	"strings"
	"sync"
)

// On macOS the app encrypts with Chromium's scheme: a password in the login
// keychain ("Claude Safe Storage"), stretched with PBKDF2, and values prefixed
// "v10" encrypted with AES-128-CBC. macOS asks once whether the switcher may
// read that keychain item.

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
	out, err := exec.Command("security", "find-generic-password", "-w", "-s", "Claude Safe Storage").Output()
	if err != nil {
		return nil, errors.New(tr("could not read \"Claude Safe Storage\" from the keychain", "无法从钥匙串读取“Claude Safe Storage”"))
	}
	return pbkdf2.Key(sha1.New, strings.TrimSpace(string(out)), []byte("saltysalt"), 1003, 16)
}

func decryptValue(data []byte) ([]byte, error) {
	if len(data) < 3+aes.BlockSize || string(data[:3]) != "v10" {
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
	ct := data[3:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, errors.New(tr("bad ciphertext length", "加密数据长度无效"))
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(plain, ct)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(plain) {
		return nil, errors.New(tr("bad padding", "加密数据填充无效"))
	}
	return plain[:len(plain)-pad], nil
}
