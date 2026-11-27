package service

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/pathutil"
)

// 「需要关注」（GET /system/attention）用到的日志尾部提取。查库与角色裁剪在 handler/system_attention.go。

// attentionLogTailBytes 是取日志尾部的字节数：只为找最后一条错误行，8 KB 足够，
// 不能把整份日志读进内存 —— 单条日志最大可到几百 MB（max_log_content_size）。
const attentionLogTailBytes = 8 * 1024

// AttentionDetailMaxRunes 是 detail 文案的最大字数（design §4.1：≤120 字）。
const AttentionDetailMaxRunes = 120

// attentionDetailCache 按日志 ID 缓存已经算好的 detail。
// 已结束的日志内容不会再变，而「需要关注」每 60 秒轮询一次，不缓存的话每次都要把失败日志解压一遍。
// 超过上限就整体清空重来，简单且不会无限增长。
var (
	attentionDetailCacheMu sync.Mutex
	attentionDetailCache   = map[uint]string{}
)

const attentionDetailCacheLimit = 512

// ansiEscapePattern 匹配终端颜色等 ANSI 转义序列，脚本输出里很常见，留着会让文案出现乱码。
var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// attentionErrorKeywords：判断一行「像不像错误信息」的关键词，全部按小写比较。
var attentionErrorKeywords = []string{
	"error", "exception", "traceback", "fail", "fatal", "timeout", "timed out", "denied", "refused", "not found",
	"错误", "异常", "失败", "超时", "拒绝", "找不到", "不存在",
}

// TaskLogAttentionDetail 取一条已结束日志的「最近一次错误」文案，读不到内容时返回空串。
// 先查缓存，命中就连日志行都不读（content 列可能很大）。
func TaskLogAttentionDetail(logID uint, logDir string) string {
	attentionDetailCacheMu.Lock()
	if cached, ok := attentionDetailCache[logID]; ok {
		attentionDetailCacheMu.Unlock()
		return cached
	}
	attentionDetailCacheMu.Unlock()

	var taskLog model.TaskLog
	if err := database.DB.Select("id", "content", "log_path").First(&taskLog, logID).Error; err != nil {
		return ""
	}

	tail := ""
	if taskLog.Content != "" {
		// 已归档的日志：zlib + base64。流式解压，只留最后 8 KB。
		tail = tailOfCompressedLog(taskLog.Content)
	} else if taskLog.LogPath != nil && *taskLog.LogPath != "" {
		tail = tailOfLogFile(*taskLog.LogPath, logDir)
	}
	detail := PickAttentionDetail(tail)

	attentionDetailCacheMu.Lock()
	if len(attentionDetailCache) >= attentionDetailCacheLimit {
		attentionDetailCache = map[uint]string{}
	}
	attentionDetailCache[taskLog.ID] = detail
	attentionDetailCacheMu.Unlock()
	return detail
}

// PickAttentionDetail 从一段日志文本的末尾往前找第一条带错误特征的非空行；
// 一条都没有就退回最后一条非空行。面板自己写的「=== 开始执行 / 执行结束 ===」分隔行不算。
// 结果去掉 ANSI 转义、截到 120 字。
func PickAttentionDetail(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(ansiEscapePattern.ReplaceAllString(text, ""), "\n")
	lastNonEmpty := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
		if line == "" || strings.HasPrefix(line, "=== ") {
			continue
		}
		// #187：conc / desi 的账号说明行是面板自己打的，不是失败原因，不能当「需要关注」的详情。
		// 只跳这两种，不整体套 isPanelMetaLine（其它面板行如「[脚本进程被信号终止：…]」对诊断有用）。
		if strings.HasPrefix(line, "[按账号并发]") || strings.HasPrefix(line, "[指定账号]") {
			continue
		}
		if lastNonEmpty == "" {
			lastNonEmpty = line
		}
		lower := strings.ToLower(line)
		for _, keyword := range attentionErrorKeywords {
			if strings.Contains(lower, keyword) {
				return truncateAttentionDetail(line)
			}
		}
	}
	return truncateAttentionDetail(lastNonEmpty)
}

func truncateAttentionDetail(line string) string {
	runes := []rune(line)
	if len(runes) <= AttentionDetailMaxRunes {
		return line
	}
	return string(runes[:AttentionDetailMaxRunes-1]) + "…"
}

// tailOfCompressedLog 流式解压 task_logs.content（zlib + base64），只保留最后 attentionLogTailBytes 字节。
func tailOfCompressedLog(encoded string) string {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	defer reader.Close()

	tail := make([]byte, 0, attentionLogTailBytes*2)
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := reader.Read(chunk)
		if n > 0 {
			tail = append(tail, chunk[:n]...)
			// 超过两倍上限才裁一次，避免每读一块都搬内存
			if len(tail) > attentionLogTailBytes*2 {
				tail = append(tail[:0], tail[len(tail)-attentionLogTailBytes:]...)
			}
		}
		if readErr != nil {
			break
		}
	}
	if len(tail) > attentionLogTailBytes {
		tail = tail[len(tail)-attentionLogTailBytes:]
	}
	return string(tail)
}

// tailOfLogFile 读运行时日志文件的最后 attentionLogTailBytes 字节。路径校验与 ReadLogFile 一致，不能逃出日志目录。
func tailOfLogFile(logPath, logDir string) string {
	fullPath := logPath
	if !filepath.IsAbs(logPath) {
		fullPath = filepath.Join(logDir, logPath)
	}
	absPath, err := pathutil.ResolveWithinBase(logDir, fullPath, true)
	if err != nil {
		return ""
	}
	file, err := os.Open(absPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - attentionLogTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(file, attentionLogTailBytes))
	if err != nil {
		return ""
	}
	return string(data)
}
