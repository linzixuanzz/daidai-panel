package service

import (
	"bufio"
	"io"
	"log"
	"regexp"
	"strings"
	"time"
)

const (
	PanelLogLevelDebug = "debug"
	PanelLogLevelInfo  = "info"
	PanelLogLevelWarn  = "warn"
	PanelLogLevelError = "error"
)

var panelLogLinePattern = regexp.MustCompile(`^\[(DEBUG|INFO|WARN|ERROR)\]\s+`)
var panelGINLinePattern = regexp.MustCompile(`^\[GIN\]\s+\d{4}/\d{2}/\d{2}\s+-\s+\d{2}:\d{2}:\d{2}\s+\|\s+(\d{3})\s+\|\s+(.+?)\s+\|\s+([^|]+?)\s+\|\s+([A-Z]+)\s+\"([^\"]+)\"$`)
var panelLogLevelPriority = map[string]int{
	PanelLogLevelDebug: 10,
	PanelLogLevelInfo:  20,
	PanelLogLevelWarn:  30,
	PanelLogLevelError: 40,
}

type PanelLogFilterWriter struct {
	dst io.Writer
}

func NewPanelLogFilterWriter(dst io.Writer) *PanelLogFilterWriter {
	return &PanelLogFilterWriter{dst: dst}
}

func (w *PanelLogFilterWriter) Write(p []byte) (int, error) {
	text := string(p)
	if shouldSuppressPanelStartupLog(text) {
		return len(p), nil
	}

	lines := splitLogPayloadLines(text)
	if len(lines) == 0 {
		return len(p), nil
	}

	var builder strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		builder.WriteString(formatPanelLogLine(trimmed))
		builder.WriteByte('\n')
	}

	if builder.Len() == 0 {
		return len(p), nil
	}
	_, err := w.dst.Write([]byte(builder.String()))
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func splitLogPayloadLines(text string) []string {
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) == 0 && strings.TrimSpace(text) != "" {
		lines = append(lines, text)
	}
	return lines
}

func formatPanelLogLine(line string) string {
	if panelLogLinePattern.MatchString(line) {
		return line
	}
	if compacted, ok := compactGINLogLine(line); ok {
		return compacted
	}
	level := detectPanelLogLevel(line)
	return "[" + strings.ToUpper(level) + "] " + line
}

func compactGINLogLine(line string) (string, bool) {
	match := panelGINLinePattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) != 6 {
		return "", false
	}

	statusCode := strings.TrimSpace(match[1])
	clientIP := strings.TrimSpace(match[3])
	method := strings.TrimSpace(match[4])
	path := strings.TrimSpace(match[5])
	level := logLevelForStatusCode(statusCode)

	ts := time.Now().Format("2006-01-02 15:04:05")
	return "[" + strings.ToUpper(level) + "] " + ts + " [" + clientIP + "] " + method + " " + path + " 状态=" + statusCode, true
}

func normalizeGINLatency(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	return raw
}

func logLevelForStatusCode(statusCode string) string {
	switch {
	case strings.HasPrefix(statusCode, "5"):
		return PanelLogLevelError
	case strings.HasPrefix(statusCode, "4"):
		return PanelLogLevelWarn
	default:
		return PanelLogLevelInfo
	}
}

func detectPanelLogLevel(line string) string {
	// [任务删除] 的留痕会把用户可控的文字（用户名、脚本路径、删除失败的错误原文）拼进行里，只能按固定文案判级，
	// 而且必须先判失败行、再判成功行（handler/task_script_cleanup.go 的两行文案）：
	//   - 失败行固定含「失败，已保留」→ ERROR。先判它：路径、用户名或错误原文里就算出现「一并删除了脚本」，
	//     也不会被下面的成功行判定降成 INFO，按 warn/error 筛选时不会漏掉（见 R2-5）。逐段检查脚本路径失败的那行
	//     （service/task_script_cleanup.go 的 addMember）也用这句。
	//   - 成功行含「一并删除了脚本」→ 固定 INFO：用户名与文件名可能带 debug/scanner/trace 等词，会被下面的关键字
	//     启发式误判成 DEBUG——面板日志默认按 info 过滤，这条不可撤销操作的留痕在默认视图里就看不到了（见 B2）。
	// 反过来，成功行的路径里若含「失败，已保留」会被判成 ERROR：误判只朝 ERROR 方向走，默认 info 视图照样可见，可以接受。
	// 已知取舍（R3-BE-2）：受保护的只有含「失败，已保留」的行。其它 [任务删除] 失败行的错误原文（如
	// service/task_script_cleanup.go「解析脚本目录失败」那行 %v 里的绝对路径），或非删除类日志里拼进的用户可控名字
	// （如 notifier.go 发通知失败那行的渠道名），只要同时含「[任务删除]」和「一并删除了脚本」，仍会判成 INFO。
	// 没改是因为触发都要人为构造，影响只在日志级别、不涉及删不删文件；要彻底关掉，得把成功行判定换成锚在用户可控
	// 字段之前的固定结构正则（见 R2-5 的另一种修法），「失败，已保留」先判 ERROR 的逻辑保留。
	if strings.Contains(line, "[任务删除]") {
		if strings.Contains(line, "失败，已保留") {
			return PanelLogLevelError
		}
		if strings.Contains(line, "一并删除了脚本") {
			return PanelLogLevelInfo
		}
	}
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.Contains(lower, " panic"), strings.Contains(lower, "fatal"), strings.Contains(lower, " failed"), strings.Contains(lower, " error"), strings.Contains(lower, "无法"), strings.Contains(lower, "失败"):
		return PanelLogLevelError
	case strings.Contains(lower, "warn"), strings.Contains(lower, "warning"), strings.Contains(lower, "超时"), strings.Contains(lower, "不可用"), strings.Contains(lower, "跳过"):
		return PanelLogLevelWarn
	case strings.Contains(lower, "debug"), strings.Contains(lower, "trace"), strings.Contains(lower, "scanner"), strings.Contains(lower, "probe"):
		return PanelLogLevelDebug
	default:
		return PanelLogLevelInfo
	}
}

func NewGINLoggerWriter(dst io.Writer) io.Writer {
	return &ginAccessLogWriter{dst: dst}
}

type ginAccessLogWriter struct {
	dst io.Writer
}

func (w *ginAccessLogWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text == "" {
		return len(p), nil
	}

	if compacted, ok := compactGINLogLine(text); ok {
		w.dst.Write([]byte(compacted + "\n"))
		return len(p), nil
	}

	w.dst.Write([]byte(strings.TrimRight(text, "\r\n") + "\n"))
	return len(p), nil
}

func ParsePanelLogLineLevel(line string) string {
	match := panelLogLinePattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) >= 2 {
		return strings.ToLower(strings.TrimSpace(match[1]))
	}
	return detectPanelLogLevel(line)
}

func MatchPanelLogLevel(line, minimumLevel string) bool {
	minimumLevel = strings.ToLower(strings.TrimSpace(minimumLevel))
	if minimumLevel == "" {
		return true
	}

	currentLevel := ParsePanelLogLineLevel(line)
	currentPriority, currentOK := panelLogLevelPriority[currentLevel]
	minimumPriority, minimumOK := panelLogLevelPriority[minimumLevel]
	if !currentOK || !minimumOK {
		return currentLevel == minimumLevel
	}

	return currentPriority >= minimumPriority
}

func NewPanelLogger(dst io.Writer) *log.Logger {
	return log.New(NewPanelLogFilterWriter(dst), "", log.LstdFlags)
}

func shouldSuppressPanelStartupLog(text string) bool {
	markers := []string{
		"database connected:",
		"added missing column:",
		"column check completed",
		"scheduler v2 started:",
		"scheduler v2 initialized with",
		"scheduler v2 enqueued",
		"subscription scheduler initialized with",
		"resource watcher started",
		"server starting on",
	}

	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}

	return false
}
