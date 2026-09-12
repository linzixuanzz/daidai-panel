package handler

import (
	"fmt"
	"strings"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/response"

	"github.com/gin-gonic/gin"
)

// BatchAddLabels 批量给任务追加标签。
// 语义为「追加」：保留任务原有全部标签（含 分组:/subscription: 等内部标签），
// 只把新标签并进去并去重，不删除任何原标签。
// 带内部前缀（分组: / subscription:）的输入一律忽略，避免用户注入保留标签。
func (h *TaskHandler) BatchAddLabels(c *gin.Context) {
	var req struct {
		TaskIDs []uint   `json:"task_ids" binding:"required"`
		Labels  []string `json:"labels" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	// 清洗待追加的标签：trim、跳过空、忽略内部前缀、去重。
	newLabels := sanitizeIncomingLabels(req.Labels)
	if len(newLabels) == 0 {
		response.BadRequest(c, "没有可添加的有效标签")
		return
	}

	count := 0
	for _, id := range req.TaskIDs {
		var task model.Task
		if database.DB.First(&task, id).Error != nil {
			continue
		}

		merged := mergeLabels(task.GetLabels(), newLabels)
		task.SetLabelsFromSlice(merged)
		if database.DB.Model(&task).Update("labels", task.Labels).Error != nil {
			continue
		}
		count++
	}

	response.Success(c, gin.H{
		"message":       fmt.Sprintf("已为 %d 个任务添加标签", count),
		"success_count": count,
	})
}

// sanitizeIncomingLabels 清洗用户输入的待追加标签：
// trim 空白、跳过空串、忽略带内部前缀的输入、去重（保持输入顺序）。
func sanitizeIncomingLabels(labels []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(labels))
	for _, raw := range labels {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		if isInternalLabel(label) {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	return result
}

// mergeLabels 把 newLabels 追加进 existing，保留 existing 原有全部标签（含内部标签），去重。
func mergeLabels(existing, newLabels []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(existing)+len(newLabels))
	for _, raw := range existing {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	for _, label := range newLabels {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	return result
}

// subscriptionLabelPrefix 是订阅任务的归属标签前缀，值形如 subscription:12，
// 由 service.subscriptionTaskLabel 在订阅同步时写入。
const subscriptionLabelPrefix = "subscription:"

// isInternalLabel 判断是否为带保留前缀的内部标签（分组: / subscription:）。
func isInternalLabel(label string) bool {
	return strings.HasPrefix(label, taskGroupLabelPrefix) || strings.HasPrefix(label, subscriptionLabelPrefix)
}
