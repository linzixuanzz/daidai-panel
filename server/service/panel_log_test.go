package service

import (
	"strings"
	"testing"
)

func TestMatchPanelLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		level string
		want  bool
	}{
		{name: "info matches info threshold", line: "[INFO] started", level: "info", want: true},
		{name: "warn matches info threshold", line: "[WARN] warn text", level: "info", want: true},
		{name: "debug filtered by info threshold", line: "[DEBUG] debug text", level: "info", want: false},
		{name: "error matches warn threshold", line: "[ERROR] boom", level: "warn", want: true},
		{name: "warn filtered by error threshold", line: "[WARN] not error", level: "error", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchPanelLogLevel(tt.line, tt.level); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

// B2：删除任务连脚本的成功留痕固定判 INFO，不因用户可控的文件名 / 用户名（含 debug/scanner/trace 等词）
// 被误判成 DEBUG 而在默认视图里看不到；remove_failed 那行含「失败」仍判 ERROR。
func TestDetectPanelLogLevelForScriptDeletion(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "success line with scanner in path",
			line: "2026/05/11 23:14:59 [任务删除] alice(127.0.0.1) 删除任务 [3] 时一并删除了脚本 tools/port_scanner.py",
			want: PanelLogLevelInfo,
		},
		{
			name: "success line with debug in path",
			line: "2026/05/11 23:14:59 [任务删除] bob(10.0.0.2) 删除任务 [7] 时一并删除了脚本 debug.py",
			want: PanelLogLevelInfo,
		},
		{
			name: "success line with 失败 in the file name",
			line: "2026/05/11 23:14:59 [任务删除] carol(10.0.0.3) 删除任务 [9] 时一并删除了脚本 失败重试.py",
			want: PanelLogLevelInfo,
		},
		{
			name: "success line with trace-like username",
			line: "2026/05/11 23:14:59 [任务删除] tracey(10.0.0.4) 删除任务 [1] 时一并删除了脚本 ok.py",
			want: PanelLogLevelInfo,
		},
		{
			name: "remove failed line stays error",
			line: "2026/05/11 23:14:59 [任务删除] alice(127.0.0.1) 删除任务 [3] 时一并删除脚本 debug.py 失败，已保留: some error",
			want: PanelLogLevelError,
		},
		// R2-5：失败行里用户可控的文字含成功行的标记串，也不能被降成 INFO。
		{
			name: "remove failed line whose path contains the success marker",
			line: "2026/05/11 23:14:59 [任务删除] alice(127.0.0.1) 删除任务 [3] 时一并删除脚本 备份/一并删除了脚本.sh 失败，已保留: remove /data/scripts/备份/一并删除了脚本.sh: permission denied",
			want: PanelLogLevelError,
		},
		{
			name: "remove failed line whose username is the success marker",
			line: "2026/05/11 23:14:59 [任务删除] 一并删除了脚本(10.0.0.5) 删除任务 [4] 时一并删除脚本 a.py 失败，已保留: permission denied",
			want: PanelLogLevelError,
		},
		{
			name: "remove failed line where only the error text contains the success marker",
			line: "2026/05/11 23:14:59 [任务删除] alice(127.0.0.1) 删除任务 [5] 时一并删除脚本 a.py 失败，已保留: remove /srv/一并删除了脚本/a.py: The process cannot access the file",
			want: PanelLogLevelError,
		},
		// 选定的取舍：成功行的路径里含「失败，已保留」会被判成 ERROR。误判只朝 ERROR 走，默认 info 视图照样可见。
		{
			name: "success line whose path contains the failure marker is reported as error",
			line: "2026/05/11 23:14:59 [任务删除] alice(127.0.0.1) 删除任务 [6] 时一并删除了脚本 失败，已保留.py",
			want: PanelLogLevelError,
		},
		// R2-1：逐段检查脚本路径失败的留痕同样判 ERROR，即使路径里含成功行的标记串。
		{
			name: "link check failure line is error even with the success marker in the path",
			line: "2026/05/11 23:14:59 [任务删除] 逐段检查任务 [7] 的脚本路径 一并删除了脚本/a.py 失败，已保留这个文件: lstat /data/scripts/一并删除了脚本: permission denied",
			want: PanelLogLevelError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectPanelLogLevel(tt.line); got != tt.want {
				t.Fatalf("detectPanelLogLevel(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestFormatPanelLogLineCompactsGINAccessLog(t *testing.T) {
	line := `[GIN] 2026/05/11 - 23:14:59 | 200 |    8.576817ms | 116.162.227.223 | GET      "/api/tasks?page=1&page_size=20"`
	got := formatPanelLogLine(line)
	if !strings.HasPrefix(got, "[INFO] ") {
		t.Fatalf("expected line to start with [INFO], got %q", got)
	}
	for _, part := range []string{"[116.162.227.223]", "GET", "/api/tasks?page=1&page_size=20", "状态=200"} {
		if !strings.Contains(got, part) {
			t.Fatalf("expected line to contain %q, got %q", part, got)
		}
	}
}
