package service

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"daidai-panel/config"
)

// ErrBackupUploadInvalid 表示上传的文件内容不是恢复流程能识别的备份。
// handler 据此回 400；其余错误（磁盘写不进去等）属于服务端问题，回 500。
var ErrBackupUploadInvalid = errors.New("备份文件内容无效")

// backupUploadTempPrefix 是上传过程中临时文件的名字前缀。临时文件与正式备份放在同一个目录里，
// 这样校验通过后改名是同盘原子操作；列表接口按这个前缀把它们藏起来，用户看不到半成品。
const backupUploadTempPrefix = ".upload-"

func invalidBackupUpload(reason string) error {
	return fmt.Errorf("%w：%s", ErrBackupUploadInvalid, reason)
}

// StoreUploadedBackup 把上传的备份先写成备份目录里的临时文件，校验内容后再改名为正式文件名。
// 校验不通过删掉临时文件、返回 ErrBackupUploadInvalid；同名文件已存在时自动改名（name-1.tgz、name-2.tar.gz…），
// 不再静默覆盖用户原有的备份。返回实际落盘的文件名。
func StoreUploadedBackup(src io.Reader, filename string) (string, error) {
	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}

	tmp, err := os.CreateTemp(backupDir, backupUploadTempPrefix+"*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	// 成功改名后临时路径已不存在，这里的删除只在中途失败时真正生效。
	defer os.Remove(tmpPath)

	_, copyErr := io.Copy(tmp, src)
	closeErr := tmp.Close()
	if copyErr != nil {
		return "", fmt.Errorf("保存文件失败: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("保存文件失败: %w", closeErr)
	}
	// CreateTemp 建出来是 0600，与面板自己生成的备份（0644）保持一致。
	_ = os.Chmod(tmpPath, 0o644)

	if err := validateBackupUploadFile(tmpPath, filename); err != nil {
		return "", err
	}

	finalName, err := reserveBackupFilename(backupDir, filename)
	if err != nil {
		return "", err
	}
	// 占位文件已经用 O_EXCL 抢到了名字，这里改名覆盖的只是自己的空占位。
	if err := os.Rename(tmpPath, filepath.Join(backupDir, finalName)); err != nil {
		_ = os.Remove(filepath.Join(backupDir, finalName))
		return "", fmt.Errorf("保存文件失败: %w", err)
	}
	return finalName, nil
}

// splitBackupFilename 把文件名拆成「主干 + 扩展名」，.tar.gz 这种双扩展名要整体保住。
func splitBackupFilename(filename string) (string, string) {
	if strings.HasSuffix(strings.ToLower(filename), ".tar.gz") {
		cut := len(filename) - len(".tar.gz")
		return filename[:cut], filename[cut:]
	}
	ext := filepath.Ext(filename)
	return filename[:len(filename)-len(ext)], ext
}

// reserveBackupFilename 找一个还没被占用的文件名，并用 O_EXCL 建一个空占位把它抢下来，
// 避免两个并发上传挑中同一个名字后互相覆盖。
func reserveBackupFilename(backupDir, filename string) (string, error) {
	stem, ext := splitBackupFilename(filename)
	for i := 0; i < 1000; i++ {
		candidate := filename
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		file, err := os.OpenFile(filepath.Join(backupDir, candidate), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			file.Close()
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("保存文件失败: %w", err)
		}
	}
	return "", fmt.Errorf("同名备份过多，请改个文件名再上传")
}

// validateBackupUploadFile 按 restoreBackupFile 的真实解析前提检查上传内容：
//   - 文件名以 .enc 结尾：恢复时先按扩展名解密。没有密码验不了内容，只验长度至少放得下加密头；
//     新旧两种加密格式都接受（见 backup.go 的 encryptData / decryptData）。
//   - 其余：恢复时不看扩展名，按内容嗅探——gzip 开头走 restoreArchiveBytes，「{」开头走 restoreLegacyJSONBytes。
//     这里用同样的嗅探规则，分别校验压缩包与旧版 JSON 的结构。
func validateBackupUploadFile(filePath, filename string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("读取上传文件失败: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("读取上传文件失败: %w", err)
	}

	if strings.HasSuffix(strings.ToLower(filename), ".enc") {
		minSize := int64(backupEncryptedMinSize)
		head := make([]byte, len(backupEncryptMagic))
		if n, _ := io.ReadFull(file, head); n == len(head) && bytes.Equal(head, backupEncryptMagic) {
			minSize = int64(len(backupEncryptMagic) + backupEncryptSaltSize + backupGCMNonceSize + backupGCMTagSize)
		}
		if info.Size() < minSize {
			return invalidBackupUpload("加密备份长度不足，文件可能已损坏")
		}
		return nil
	}

	reader := bufio.NewReaderSize(file, 4096)
	head, _ := reader.Peek(4096)
	if looksLikeGzip(head) {
		return validateBackupArchiveStream(reader)
	}
	// 与 looksLikeJSON 同口径：去掉开头空白后以「{」开头。只看开头一段，不是 JSON 的大文件不必整个读进内存。
	if bytes.HasPrefix(bytes.TrimLeftFunc(head, unicode.IsSpace), []byte("{")) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("读取上传文件失败: %w", err)
		}
		return validateLegacyBackupJSON(data)
	}
	return invalidBackupUpload("无法识别的备份格式，仅支持面板导出的 .tgz / .tar.gz / .json / .enc 备份或青龙备份")
}

// validateBackupArchiveStream 对应 restoreArchiveBytes：能解 gzip + tar，
// 根目录有 manifest.json 时它必须能解析且至少选了一项（恢复时清单为空会报「备份内容为空」）；
// 没有 manifest.json 时按青龙备份识别，要求存在同时含 config 与 db 的目录（resolveQingLongDataDir 的前提）。
func validateBackupArchiveStream(reader io.Reader) error {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return invalidBackupUpload("不是有效的 gzip 压缩包")
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	hasManifest := false
	// 解包后会出现在磁盘上的路径（恢复只落地目录与普通文件），用来判断青龙备份的目录结构。
	present := map[string]bool{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return invalidBackupUpload("压缩包内容读取失败，文件可能已损坏或不是 tar 格式")
		}

		// 与 extractTarGzBytes 一致：路径跳出解包目录的备份恢复时会被拒绝，上传时就挡掉。
		slashName := strings.ReplaceAll(header.Name, "\\", "/")
		cleanName := path.Clean(strings.TrimPrefix(slashName, "/"))
		if cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return invalidBackupUpload("备份包含非法路径: " + header.Name)
		}
		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
			continue
		}

		for p := cleanName; p != "." && p != "/" && p != ""; p = path.Dir(p) {
			present[p] = true
		}

		if cleanName == "manifest.json" && header.Typeflag == tar.TypeReg {
			var manifest BackupManifest
			if err := json.NewDecoder(tarReader).Decode(&manifest); err != nil {
				return invalidBackupUpload("备份清单 manifest.json 解析失败")
			}
			if !manifest.Selection.Any() {
				return invalidBackupUpload("备份清单里没有任何可恢复的内容")
			}
			hasManifest = true
		}
	}
	// tar 读到结束块就停了，gzip 的校验和要读到流末尾才会核对；读完它，截断或损坏的压缩包在这里暴露。
	if _, err := io.Copy(io.Discard, gzipReader); err != nil {
		return invalidBackupUpload("压缩包已损坏")
	}

	if hasManifest {
		return nil
	}
	for p := range present {
		if path.Base(p) == "config" && present[path.Join(path.Dir(p), "db")] {
			return nil
		}
	}
	return invalidBackupUpload("压缩包里既没有面板备份清单 manifest.json，也不是青龙备份")
}

// validateLegacyBackupJSON 对应 restoreLegacyJSONBytes：能按 LegacyBackupData 解析，
// 且带着旧版备份必有的数据键（这几个字段在旧版里没有 omitempty，真实的旧备份一定会写出来）。
// 只认语法不认结构的话，一个「{}」也能通过，恢复时会把任务、变量、订阅、配置全部清空。
func validateLegacyBackupJSON(data []byte) error {
	var legacy LegacyBackupData
	if err := json.Unmarshal(data, &legacy); err != nil {
		return invalidBackupUpload("JSON 备份解析失败")
	}
	var probe struct {
		Tasks         json.RawMessage `json:"tasks"`
		EnvVars       json.RawMessage `json:"env_vars"`
		Subscriptions json.RawMessage `json:"subscriptions"`
		SystemConfigs json.RawMessage `json:"system_configs"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return invalidBackupUpload("JSON 备份解析失败")
	}
	if probe.Tasks == nil && probe.EnvVars == nil && probe.Subscriptions == nil && probe.SystemConfigs == nil {
		return invalidBackupUpload("不是面板导出的 JSON 备份")
	}
	return nil
}
