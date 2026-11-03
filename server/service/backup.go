package service

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"daidai-panel/config"
)

type ScriptFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func collectScripts(scriptsDir string) []ScriptFile {
	var files []ScriptFile
	allowedExts := map[string]bool{".js": true, ".mjs": true, ".py": true, ".ts": true, ".sh": true}

	filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if ShouldHideScriptTreePath(scriptsDir, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if ShouldHideScriptTreePath(scriptsDir, path) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !allowedExts[ext] {
			return nil
		}
		if info.Size() > 10*1024*1024 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(scriptsDir, path)
		rel = filepath.ToSlash(rel)
		files = append(files, ScriptFile{
			Path:    rel,
			Content: base64.StdEncoding.EncodeToString(data),
		})
		return nil
	})
	return files
}

func restoreScripts(scriptsDir string, scripts []ScriptFile) {
	for _, sf := range scripts {
		if strings.Contains(sf.Path, "..") {
			continue
		}
		// 逐段遍历：避免旧 JSON 备份把 SmallWorld/.git/... 之类的内容写回脚本目录
		if ShouldHideScriptTreeRelativePath(sf.Path) {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(sf.Content)
		if err != nil {
			continue
		}
		fullPath := filepath.Join(scriptsDir, filepath.FromSlash(sf.Path))
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, data, 0755)
	}
}

func CreateBackup(options BackupCreateOptions) (string, error) {
	return createBackupArchive(options)
}

// 加密备份（.enc）的两种格式：
//   - 新格式（v3.4.2 起写出）：魔数头 + 16 字节随机盐 + GCM nonce + 密文，密钥用 PBKDF2-HMAC-SHA256 派生。
//     旧格式的密钥是密码做一次 SHA-256，备份文件一旦外泄，离线穷举密码几乎没有成本；PBKDF2 把每次尝试的代价抬高几十万倍。
//   - 旧格式：GCM nonce + 密文，密钥 = SHA-256(密码)。只解不写，保证老备份永远能恢复。
//
// 判断依据只有魔数头：旧格式开头是随机 nonce，恰好撞上 8 字节魔数的概率可以忽略。
var backupEncryptMagic = []byte("DDPENC02")

const (
	backupEncryptSaltSize   = 16
	backupEncryptIterations = 600000
	backupEncryptKeySize    = 32
)

// GCM 的 nonce 与认证标签长度（标准库默认值），只用来做长度下限判断。
const (
	backupGCMNonceSize = 12
	backupGCMTagSize   = 16
)

// backupEncryptedMinSize 是一个加密备份至少要有的字节数：旧格式最短也得放下 nonce + 认证标签。
// 新格式更长（多了魔数与盐），上传校验时按开头是否带魔数分别判断。
const backupEncryptedMinSize = backupGCMNonceSize + backupGCMTagSize

func deriveBackupKeyPBKDF2(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, backupEncryptIterations, backupEncryptKeySize)
}

func newBackupGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encryptData(data []byte, password string) ([]byte, error) {
	salt := make([]byte, backupEncryptSaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key, err := deriveBackupKeyPBKDF2(password, salt)
	if err != nil {
		return nil, err
	}
	gcm, err := newBackupGCM(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	// 布局：魔数 | 盐 | nonce | 密文（含认证标签）
	out := make([]byte, 0, len(backupEncryptMagic)+len(salt)+len(nonce)+len(data)+gcm.Overhead())
	out = append(out, backupEncryptMagic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, data, nil), nil
}

func decryptData(data []byte, password string) ([]byte, error) {
	if bytes.HasPrefix(data, backupEncryptMagic) {
		rest := data[len(backupEncryptMagic):]
		if len(rest) < backupEncryptSaltSize+backupGCMNonceSize+backupGCMTagSize {
			return nil, fmt.Errorf("密文数据过短")
		}
		salt, rest := rest[:backupEncryptSaltSize], rest[backupEncryptSaltSize:]
		key, err := deriveBackupKeyPBKDF2(password, salt)
		if err != nil {
			return nil, err
		}
		gcm, err := newBackupGCM(key)
		if err != nil {
			return nil, err
		}
		nonce, ciphertext := rest[:gcm.NonceSize()], rest[gcm.NonceSize():]
		return gcm.Open(nil, nonce, ciphertext, nil)
	}

	// 没有魔数头：v3.4.2 之前写出的旧格式，密钥是单次 SHA-256。
	key := sha256.Sum256([]byte(password))
	gcm, err := newBackupGCM(key[:])
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("密文数据过短")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func RestoreBackup(filename, password string) error {
	return restoreBackupFile(filename, password)
}

func ListBackups() ([]map[string]interface{}, error) {
	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return []map[string]interface{}{}, nil
	}

	var backups []map[string]interface{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// 上传中途的临时文件（校验通过才改名）不是备份，不列出来。
		if strings.HasPrefix(entry.Name(), backupUploadTempPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, map[string]interface{}{
			"name":       entry.Name(),
			"size":       info.Size(),
			"created_at": info.ModTime(),
		})
	}

	return backups, nil
}

func DeleteBackup(filename string) error {
	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	filePath := filepath.Join(backupDir, filepath.Base(filename))
	return os.Remove(filePath)
}
