package mcptools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 任务的新建与修改（#139）：对应 POST /tasks、PUT /tasks/:id；
// 批量设置通知开关（#149）：对应 PUT /tasks/batch/notify。
// 运行、停止、启停与 PUT /tasks/batch 那组批量操作仍在 tools_write.go。
func (t *toolset) registerTaskWriteTools(s *mcp.Server) {
	addWriteTool(s, "create_task", "新建任务",
		"新建一个任务，建好即启用：task_type 为 cron（默认）时按 cron_expression 定时运行。command 与面板任务的「命令」一样，例如 task demo/sign.py。"+
			"注意 command 会以面板的权限执行，新建后可用 run_task 立即运行一次。",
		writeHints{}, t.createTask)
	addWriteTool(s, "update_task", "修改任务",
		"按 ID 修改任务配置，只改传入的字段（名称、命令、定时规则、类型、超时、标签等），原值被覆盖。脚本改名或移动后，可以用它同步任务的 command。"+
			"不改可用环境变量范围（只能在网页或 APP 里设置）。",
		writeHints{destructive: true, idempotent: true}, t.updateTask)
	// 会覆盖已有的开关设置，按 quality-guidelines「会覆盖已有数据的也算破坏性」标 destructive。
	addWriteTool(s, "batch_set_task_notify", "批量设置任务通知",
		"批量打开或关闭任务的失败 / 成功 / 终止通知，只改传入的开关，不改任务绑定的通知渠道（没绑渠道的任务发到「默认推送」渠道）。"+
			"用 ids 指定任务，或 all: true 改全部任务（此时忽略 ids）。已经在排队的那一次执行仍用旧设置，下一次执行生效。",
		writeHints{destructive: true, idempotent: true}, t.batchSetTaskNotify)
}

// taskFields 是新建与修改共用的可选字段。指针为 nil、切片为 nil 表示没传：
// 修改时只发送传了的字段，面板按「传了才改」处理；labels 传空数组表示清空标签。
// 🔴 刻意不开放 env_scope / env_groups / env_names（#168 可用环境变量范围）：与环境变量的「重要」标记同一先例，
// 这类保护性设置不让 AI 改，只能在网页或 APP 里设。不带这三项时面板保持原值。
type taskFields struct {
	TaskType               *string  `json:"task_type,omitempty" jsonschema:"任务类型：cron（按定时规则运行，默认）、manual（只手动运行）、startup（面板启动时运行）"`
	CronExpression         *string  `json:"cron_expression,omitempty" jsonschema:"定时规则（5 段 cron，或带秒的 6 段；多条规则用换行分隔）。task_type 为 cron 时必填"`
	Timeout                *int     `json:"timeout,omitempty" jsonschema:"超时秒数（0 到 604800），0 表示不限制"`
	Labels                 []string `json:"labels,omitempty" jsonschema:"标签列表，修改时整体替换，传空数组清空。分组就是「分组:名称」形式的标签"`
	PythonVersion          *string  `json:"python_version,omitempty" jsonschema:"运行 Python 脚本用的版本，例如 3.12；留空用面板默认版本"`
	MaxRetries             *int     `json:"max_retries,omitempty" jsonschema:"失败后自动重试的次数（0 到 10），0 表示不重试"`
	RetryInterval          *int     `json:"retry_interval,omitempty" jsonschema:"重试间隔秒数（0 到 3600）"`
	RandomDelaySeconds     *int     `json:"random_delay_seconds,omitempty" jsonschema:"定时触发后随机延迟的最大秒数（0 到 86400）"`
	SuccessExitCodes       *string  `json:"success_exit_codes,omitempty" jsonschema:"视为成功的退出码，逗号分隔，默认 0"`
	NotifyOnFailure        *bool    `json:"notify_on_failure,omitempty" jsonschema:"失败时发通知"`
	NotifyOnSuccess        *bool    `json:"notify_on_success,omitempty" jsonschema:"成功时发通知"`
	NotifyOnAbort          *bool    `json:"notify_on_abort,omitempty" jsonschema:"被终止时发通知"`
	TaskBefore             *string  `json:"task_before,omitempty" jsonschema:"每次运行前先执行的 shell 命令"`
	TaskAfter              *string  `json:"task_after,omitempty" jsonschema:"每次运行后再执行的 shell 命令"`
	AllowMultipleInstances *bool    `json:"allow_multiple_instances,omitempty" jsonschema:"上一次还没结束时，是否允许到点再启动一个实例"`
	LogRetentionDays       *int     `json:"log_retention_days,omitempty" jsonschema:"这个任务的日志保留天数（1-3650）；不填就跟随面板全局设置，填 0 表示改回跟随全局"`
}

type createTaskInput struct {
	Name    string `json:"name" jsonschema:"任务名称"`
	Command string `json:"command" jsonschema:"要执行的命令，例如 task demo/sign.py"`
	taskFields
}

type updateTaskInput struct {
	ID      int64   `json:"id" jsonschema:"任务 ID，可从 list_tasks 获得"`
	Name    *string `json:"name,omitempty" jsonschema:"新的任务名称"`
	Command *string `json:"command,omitempty" jsonschema:"新的命令"`
	taskFields
}

var taskTypeValues = map[string]bool{"cron": true, "manual": true, "startup": true}

// addTo 把传了的字段写进请求体，并在发出请求前挡掉明显不对的取值。
func (f taskFields) addTo(body map[string]any) error {
	if f.TaskType != nil {
		taskType := strings.ToLower(strings.TrimSpace(*f.TaskType))
		if !taskTypeValues[taskType] {
			return fmt.Errorf("task_type 只能是 cron、manual、startup，收到 %q", *f.TaskType)
		}
		body["task_type"] = taskType
	}
	setIfPresent(body, "cron_expression", f.CronExpression)
	if f.Timeout != nil {
		if *f.Timeout < 0 {
			return errors.New("timeout 不能是负数")
		}
		body["timeout"] = *f.Timeout
	}
	if f.Labels != nil {
		labels := make([]string, 0, len(f.Labels))
		for _, label := range f.Labels {
			if label = strings.TrimSpace(label); label != "" {
				labels = append(labels, label)
			}
		}
		body["labels"] = labels
	}
	setIfPresent(body, "python_version", f.PythonVersion)
	setIfPresent(body, "max_retries", f.MaxRetries)
	setIfPresent(body, "retry_interval", f.RetryInterval)
	setIfPresent(body, "random_delay_seconds", f.RandomDelaySeconds)
	setIfPresent(body, "success_exit_codes", f.SuccessExitCodes)
	setIfPresent(body, "notify_on_failure", f.NotifyOnFailure)
	setIfPresent(body, "notify_on_success", f.NotifyOnSuccess)
	setIfPresent(body, "notify_on_abort", f.NotifyOnAbort)
	setIfPresent(body, "task_before", f.TaskBefore)
	setIfPresent(body, "task_after", f.TaskAfter)
	setIfPresent(body, "allow_multiple_instances", f.AllowMultipleInstances)
	// 面板侧把 0 和负数归成「跟随全局」，所以这里原样透传即可：
	// 传 0 就是把任务级天数改回跟随全局，不像 random_delay_seconds 那样改不回去（issue #144 / v3.3.2）。
	setIfPresent(body, "log_retention_days", f.LogRetentionDays)
	return nil
}

// setIfPresent 只在调用方传了这个参数时写入请求体（任务、订阅的修改接口都是「传了才改」）。
func setIfPresent[T any](body map[string]any, key string, value *T) {
	if value != nil {
		body[key] = *value
	}
}

func (t *toolset) createTask(ctx context.Context, in createTaskInput) (any, error) {
	name := strings.TrimSpace(in.Name)
	command := strings.TrimSpace(in.Command)
	if name == "" || command == "" {
		return nil, errors.New("name 与 command 都不能为空")
	}
	body := map[string]any{"name": name, "command": command}
	if err := in.taskFields.addTo(body); err != nil {
		return nil, err
	}
	result, err := t.call(ctx, http.MethodPost, "/tasks", nil, body)
	if err != nil {
		return nil, err
	}
	return taskMutationOutput(result), nil
}

func (t *toolset) updateTask(ctx context.Context, in updateTaskInput) (any, error) {
	if err := requireID("id", in.ID); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errors.New("name 不能改成空")
		}
		body["name"] = name
	}
	if in.Command != nil {
		command := strings.TrimSpace(*in.Command)
		if command == "" {
			return nil, errors.New("command 不能改成空")
		}
		body["command"] = command
	}
	if err := in.taskFields.addTo(body); err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("没有要修改的字段：至少传一个 id 以外的参数")
	}
	result, err := t.call(ctx, http.MethodPut, fmt.Sprintf("/tasks/%d", in.ID), nil, body)
	if err != nil {
		return nil, err
	}
	return taskMutationOutput(result), nil
}

type batchSetTaskNotifyInput struct {
	IDs             []int64 `json:"ids,omitempty" jsonschema:"任务 ID 列表（1 到 100 个），可从 list_tasks 获得；all 为 true 时不用传"`
	All             bool    `json:"all,omitempty" jsonschema:"true 表示改全部任务（不受任何筛选影响），此时忽略 ids"`
	NotifyOnFailure *bool   `json:"notify_on_failure,omitempty" jsonschema:"失败时通知：true 打开、false 关闭，不传则不修改"`
	NotifyOnSuccess *bool   `json:"notify_on_success,omitempty" jsonschema:"成功时通知：true 打开、false 关闭，不传则不修改"`
	NotifyOnAbort   *bool   `json:"notify_on_abort,omitempty" jsonschema:"被终止时通知：true 打开、false 关闭，不传则不修改"`
}

// batchSetTaskNotify 转发到 PUT /tasks/batch/notify（#149）。参数名沿用本包其它批量工具的 ids，
// 发给面板时换成接口的 task_ids；三个开关用指针，false 也要原样发出去（那是「批量关闭」）。
// 面板一个任务都没命中时回 404，call 会把它翻成工具错误，不会当成功转述给 Agent。
func (t *toolset) batchSetTaskNotify(ctx context.Context, in batchSetTaskNotifyInput) (any, error) {
	body := map[string]any{}
	setIfPresent(body, "notify_on_failure", in.NotifyOnFailure)
	setIfPresent(body, "notify_on_success", in.NotifyOnSuccess)
	setIfPresent(body, "notify_on_abort", in.NotifyOnAbort)
	if len(body) == 0 {
		return nil, errors.New("请至少传 notify_on_failure、notify_on_success、notify_on_abort 其中一个")
	}
	if in.All {
		body["all"] = true
	} else {
		if len(in.IDs) == 0 || len(in.IDs) > maxBatchTaskIDs {
			return nil, fmt.Errorf("ids 需要 1 到 %d 个任务 ID；要改全部任务请传 all: true", maxBatchTaskIDs)
		}
		for _, id := range in.IDs {
			if err := requireID("ids 中的任务 ID", id); err != nil {
				return nil, err
			}
		}
		body["task_ids"] = in.IDs
	}
	result, err := t.call(ctx, http.MethodPut, "/tasks/batch/notify", nil, body)
	if err != nil {
		return nil, err
	}
	return pickFields(result, "message", "success_count"), nil
}

func taskMutationOutput(result map[string]any) map[string]any {
	out := map[string]any{"message": result["message"]}
	if task, ok := result["data"].(map[string]any); ok {
		out["task"] = taskDetail(task)
	}
	return out
}
