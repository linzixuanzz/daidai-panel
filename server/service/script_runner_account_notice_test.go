package service

import (
	"runtime"
	"strings"
	"testing"

	"daidai-panel/config"
	"daidai-panel/testutil"
)

// #187：conc / desi 的账号说明行内容要对（变量名、总数、序号），normal 模式不出说明。
func TestTaskAccountModeNoticeContent(t *testing.T) {
	selections := []taskAccountSelection{{Index: 1}, {Index: 2}, {Index: 3}}

	conc := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeConc, EnvName: "CK"}, 3, selections)
	wantConc := "[按账号并发] CK 共 3 个账号（只算已启用的，按环境变量页顺序），本次运行第 1、2、3 个，每个账号单独一个进程。每个进程只拿到自己那一个账号，脚本日志里的「账号1」指的是该进程分到的账号，以行首 [CK#序号] 为准"
	if conc != wantConc {
		t.Fatalf("conc 说明行不对：\n got %q\nwant %q", conc, wantConc)
	}

	desi := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeDesi, EnvName: "CK"}, 3, selections[1:])
	wantDesi := "[指定账号] 只把 CK 共 3 个账号（只算已启用的，按环境变量页顺序）中的第 2、3 个交给脚本，脚本自己的编号会从 1 开始"
	if desi != wantDesi {
		t.Fatalf("desi 说明行不对：\n got %q\nwant %q", desi, wantDesi)
	}

	for _, mode := range []commandExecutionMode{commandModeNormal, commandModeNow} {
		if got := taskAccountModeNotice(&CommandExecutionPlan{Mode: mode, EnvName: "CK"}, 3, selections); got != "" {
			t.Fatalf("%s 模式不该有账号说明，实际 %q", mode, got)
		}
	}
}

// 序号超过 20 个只列前 20 个，后面写「等 N 个」。
func TestTaskAccountModeNoticeTruncatesLongIndexList(t *testing.T) {
	selections := make([]taskAccountSelection, 0, 25)
	for i := 1; i <= 25; i++ {
		selections = append(selections, taskAccountSelection{Index: i})
	}

	got := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeDesi, EnvName: "CK"}, 30, selections)
	want := "[指定账号] 只把 CK 共 30 个账号（只算已启用的，按环境变量页顺序）中的第 1、2、3、4、5、6、7、8、9、10、11、12、13、14、15、16、17、18、19、20 等 25 个交给脚本，脚本自己的编号会从 1 开始"
	if got != want {
		t.Fatalf("超过 20 个序号时应截断：\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "21") {
		t.Fatalf("第 21 个以后的序号不该出现：%q", got)
	}

	// 正好 20 个不截断
	exact := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeConc, EnvName: "CK"}, 20, selections[:20])
	if strings.Contains(exact, "等 ") || !strings.Contains(exact, "第 1、2、3、4、5、6、7、8、9、10、11、12、13、14、15、16、17、18、19、20 个，") {
		t.Fatalf("正好 20 个时应完整列出且不写「等」：%q", exact)
	}
}

// 两种说明行都是面板自己打进任务日志的，必须登记到 panelMetaLinePrefixes（契约见该变量注释），
// 否则每次 conc / desi 成功都会挤进成功通知的日志摘录。取自真实生成函数，免得字面量与实现漂移。
func TestTaskAccountModeNoticeRegisteredAsPanelMeta(t *testing.T) {
	selections := []taskAccountSelection{{Index: 1}, {Index: 2}}
	lines := []string{
		taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeConc, EnvName: "CK"}, 2, selections),
		taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeDesi, EnvName: "CK"}, 2, selections),
	}
	for _, line := range lines {
		if !isPanelMetaLine(line) {
			t.Fatalf("账号说明行 %q 没有登记到 panelMetaLinePrefixes", line)
		}
	}

	summary := summarizeTaskSuccessOutput(strings.Join(append(lines, "签到: 成功"), "\n"))
	if summary != "签到: 成功" {
		t.Fatalf("成功摘录里不该有账号说明行，实际 %q", summary)
	}
}

// 端到端：经 RunCommandWithPlan 真跑一遍，conc 打一次说明（不带 [CK#N] 前缀、在各账号输出之前），
// desi 打一次说明，normal 不打。用托管依赖命令的假可执行文件跑，Windows（.cmd）和 Linux（sh）都能跑。
func TestRunCommandEmitsAccountModeNotice(t *testing.T) {
	testutil.SetupTestEnv(t)

	pythonVersion := DefaultPythonVersion()
	venvBin := resolveManagedVenvBin(ManagedPythonVenvDir(pythonVersion))
	envEcho := "echo acct:$CK"
	if runtime.GOOS == "windows" {
		envEcho = "echo acct:%CK%"
	}
	writeFakeExecutable(t, venvBin, "accountprobe", []string{envEcho})
	// desi / normal 收到的 CK 带 &，Windows 的 cmd 会把它当命令分隔符，所以这两种只用不读 CK 的假命令
	writeFakeExecutable(t, venvBin, "plainprobe", []string{"echo plain-ok"})

	envVars := map[string]string{
		"DAIDAI_PYTHON_VERSION": pythonVersion,
		"CK":                    "a1&a2&a3",
	}
	run := func(command string) string {
		t.Helper()
		plan, err := ParseCommandExecutionPlan(command, config.C.Data.ScriptsDir)
		if err != nil {
			t.Fatalf("parse %q: %v", command, err)
		}
		// 输出回调可能来自 conc 的多个协程，但 appendLine 内部已加锁串行化，这里直接写即可
		var output strings.Builder
		result, _, err := RunCommandWithPlan(plan, 10, envVars, 1<<20, func(chunk string) { output.WriteString(chunk) })
		if err != nil {
			t.Fatalf("run %q: %v output=%s", command, err, output.String())
		}
		if result.ReturnCode != 0 {
			t.Fatalf("run %q 退出码 %d output=%s", command, result.ReturnCode, output.String())
		}
		return output.String()
	}

	concOutput := run(`task accountprobe conc CK 1-3`)
	concNotice := "[按账号并发] CK 共 3 个账号（只算已启用的，按环境变量页顺序），本次运行第 1、2、3 个，每个账号单独一个进程。"
	if strings.Count(concOutput, concNotice) != 1 {
		t.Fatalf("conc 说明行应恰好出现一次，output=%q", concOutput)
	}
	if !strings.HasPrefix(concOutput, concNotice) {
		t.Fatalf("conc 说明行应在各账号进程输出之前、且行首没有 [CK#N] 前缀，output=%q", concOutput)
	}
	for _, want := range []string{"[CK#1] acct:a1", "[CK#2] acct:a2", "[CK#3] acct:a3"} {
		if !strings.Contains(concOutput, want) {
			t.Fatalf("conc 每个进程应只拿到自己的账号，缺 %q，output=%q", want, concOutput)
		}
	}

	desiOutput := run(`task plainprobe desi CK 2-3`)
	desiNotice := "[指定账号] 只把 CK 共 3 个账号（只算已启用的，按环境变量页顺序）中的第 2、3 个交给脚本，脚本自己的编号会从 1 开始\n"
	if strings.Count(desiOutput, desiNotice) != 1 {
		t.Fatalf("desi 说明行应恰好出现一次，output=%q", desiOutput)
	}

	normalOutput := run(`task plainprobe`)
	if strings.Contains(normalOutput, "[按账号并发]") || strings.Contains(normalOutput, "[指定账号]") {
		t.Fatalf("normal 模式不该有账号说明，output=%q", normalOutput)
	}
	if !strings.Contains(normalOutput, "plain-ok") {
		t.Fatalf("normal 模式应正常执行，output=%q", normalOutput)
	}
}

// #187 复核 R8：账号说明行是面板自己打的，不能进失败摘要。
// 脚本没输出就失败时它是唯一一行，不能被当成摘要；只有一行错误时它也不能成为通用摘要的「上下文」。
func TestTaskAccountModeNoticeExcludedFromFailureSummary(t *testing.T) {
	selections := []taskAccountSelection{{Index: 2}, {Index: 3}}
	desi := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeDesi, EnvName: "CK"}, 3, selections)
	conc := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeConc, EnvName: "CK"}, 3, selections)

	for _, notice := range []string{desi, conc} {
		// 脚本无输出失败：只剩说明行和面板的起止行
		if summary := summarizeTaskFailureOutput("=== 开始执行 ===\n" + notice + "\n=== 执行结束 ==="); summary != "" {
			t.Fatalf("只有说明行时失败摘要应为空，got %q", summary)
		}
		// 一行错误：说明行不能当「上下文」
		summary := summarizeTaskFailureOutput(notice + "\nError: token expired")
		if summary != "Error: token expired" {
			t.Fatalf("说明行不该成为失败摘要的上下文，got %q", summary)
		}
	}
}

// #187 复核 R8：「需要关注」详情不选账号说明行，但照旧会选其它面板行。
func TestPickAttentionDetailSkipsAccountModeNotice(t *testing.T) {
	selections := []taskAccountSelection{{Index: 2}, {Index: 3}}
	desi := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeDesi, EnvName: "CK"}, 3, selections)
	conc := taskAccountModeNotice(&CommandExecutionPlan{Mode: commandModeConc, EnvName: "CK"}, 3, selections)

	for _, notice := range []string{desi, conc} {
		if got := PickAttentionDetail(notice + "\n"); got != "" {
			t.Fatalf("只有说明行时详情应为空，got %q", got)
		}
		if got := PickAttentionDetail("签到中\n" + notice + "\n"); got != "签到中" {
			t.Fatalf("没有错误行时应取说明行以外的最后一行，got %q", got)
		}
	}
	signalLine := "[脚本进程被信号终止：killed(9)（退出码 -1）]"
	if got := PickAttentionDetail(desi + "\n" + signalLine + "\n"); got != signalLine {
		t.Fatalf("其它面板行照旧可以当详情，got %q", got)
	}
}
