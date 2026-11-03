package service

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"testing"
)

// legacyEncryptForTest 按 v3.4.2 之前的格式加密（nonce + 密文，密钥 = SHA-256(密码)），
// 用来证明老备份升级后依然能解。
func legacyEncryptForTest(t *testing.T, data []byte, password string) []byte {
	t.Helper()
	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	return gcm.Seal(nonce, nonce, data, nil)
}

func TestBackupEncryptNewFormatRoundTrip(t *testing.T) {
	plain := []byte("daidai backup payload \x00\x01\x02 中文")
	encrypted, err := encryptData(plain, "correct horse")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.HasPrefix(encrypted, backupEncryptMagic) {
		t.Fatal("新写出的加密备份应当带魔数头")
	}
	if want := len(backupEncryptMagic) + backupEncryptSaltSize + backupGCMNonceSize + len(plain) + backupGCMTagSize; len(encrypted) != want {
		t.Fatalf("新格式长度应为 魔数+盐+nonce+明文+标签 = %d，实际 %d", want, len(encrypted))
	}

	decrypted, err := decryptData(encrypted, "correct horse")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("往返结果不一致: %q", decrypted)
	}

	// 同一密码两次加密，盐与 nonce 都随机，密文不应相同。
	again, err := encryptData(plain, "correct horse")
	if err != nil {
		t.Fatalf("encrypt again: %v", err)
	}
	if bytes.Equal(again, encrypted) {
		t.Fatal("两次加密的密文不应相同")
	}
}

func TestBackupDecryptLegacyFormatStillWorks(t *testing.T) {
	plain := []byte("legacy backup")
	legacy := legacyEncryptForTest(t, plain, "old-password")

	decrypted, err := decryptData(legacy, "old-password")
	if err != nil {
		t.Fatalf("旧格式加密备份应当仍能解密: %v", err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("旧格式解密结果不一致: %q", decrypted)
	}

	if _, err := decryptData(legacy, "wrong"); err == nil {
		t.Fatal("旧格式密码错误应当报错")
	}
}

func TestBackupDecryptRejectsWrongPasswordAndTampering(t *testing.T) {
	plain := []byte("sensitive")
	encrypted, err := encryptData(plain, "p@ss")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if _, err := decryptData(encrypted, "not-the-password"); err == nil {
		t.Fatal("密码错误应当报错")
	}

	// 分别篡改盐、nonce、密文：GCM 认证必须失败。
	offsets := []int{
		len(backupEncryptMagic),
		len(backupEncryptMagic) + backupEncryptSaltSize,
		len(encrypted) - 1,
	}
	for _, offset := range offsets {
		tampered := append([]byte(nil), encrypted...)
		tampered[offset] ^= 0x01
		if _, err := decryptData(tampered, "p@ss"); err == nil {
			t.Fatalf("篡改第 %d 字节后应当解密失败", offset)
		}
	}

	// 只剩魔数头、放不下盐与 nonce 的截断文件：报错而不是越界 panic。
	if _, err := decryptData(encrypted[:len(backupEncryptMagic)+4], "p@ss"); err == nil {
		t.Fatal("截断的新格式密文应当报错")
	}
}
