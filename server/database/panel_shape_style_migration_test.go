package database_test

import (
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

func readPanelShapeStyleRaw(t *testing.T) string {
	t.Helper()
	var value string
	if err := database.DB.Raw("SELECT value FROM system_configs WHERE `key` = ?", "panel_shape_style").Scan(&value).Error; err != nil {
		t.Fatalf("read panel_shape_style: %v", err)
	}
	return value
}

func countPanelShapeMarker(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := database.DB.Raw("SELECT COUNT(*) FROM system_configs WHERE `key` = ?", database.PanelShapeStyleMigratedConfigKey).Scan(&count).Error; err != nil {
		t.Fatalf("count marker: %v", err)
	}
	return count
}

// TestPanelShapeStyleMigratesToThemeOnlyOnce 钉住 v4.0 圆角迁移的三条验收（design §1.4 / prd 验收 6）：
//  1. 老库首次启动：已存的 panel_shape_style（不管是 square 还是 rounded）改成 theme，并写下标记；
//  2. 第二次启动：有标记就不再碰；
//  3. 迁移之后管理员改成 square，再重启（EnsureColumns + InitDefaultConfigs）仍是 square。
//
// 第 3 条是这个标记存在的全部意义：没有它，每次重启都会把管理员的选择改回 theme，而编译、其余用例都照样全绿。
func TestPanelShapeStyleMigratesToThemeOnlyOnce(t *testing.T) {
	testutil.SetupTestEnv(t)

	// 模拟 v3.x 老库：库里存着当时的值，且没有迁移标记（SetupTestEnv 不跑 EnsureColumns，标记本来就不存在）
	if err := database.DB.Exec("UPDATE system_configs SET value = 'rounded' WHERE `key` = 'panel_shape_style'").Error; err != nil {
		t.Fatalf("seed legacy value: %v", err)
	}
	if countPanelShapeMarker(t) != 0 {
		t.Fatal("seed: 迁移标记不应存在")
	}

	// 1. 首次启动
	database.EnsureColumns()
	if got := readPanelShapeStyleRaw(t); got != "theme" {
		t.Fatalf("首次启动应把 panel_shape_style 改成 theme，实际 %q", got)
	}
	if countPanelShapeMarker(t) != 1 {
		t.Fatal("首次启动后应写下迁移标记")
	}

	// 2. 第二次启动：换一个值进去模拟「标记已在」，确认不再改动
	if err := database.DB.Exec("UPDATE system_configs SET value = 'rounded' WHERE `key` = 'panel_shape_style'").Error; err != nil {
		t.Fatalf("reset value: %v", err)
	}
	database.EnsureColumns()
	if got := readPanelShapeStyleRaw(t); got != "rounded" {
		t.Fatalf("有标记时第二次启动不应再改，实际 %q", got)
	}
	if countPanelShapeMarker(t) != 1 {
		t.Fatal("标记只应有一行")
	}

	// 3. 管理员改成直角后重启，仍是直角
	if err := model.SetConfig("panel_shape_style", "square"); err != nil {
		t.Fatalf("admin set square: %v", err)
	}
	database.EnsureColumns()
	model.InitDefaultConfigs()
	if got := model.GetRegisteredConfig("panel_shape_style"); got != "square" {
		t.Fatalf("迁移后管理员选的直角重启后应保持，实际 %q", got)
	}
}

// 新装实例：EnsureColumns 时还没有 panel_shape_style 这一行，迁移只写标记；
// 之后 InitDefaultConfigs 按注册表默认建出 theme。
func TestPanelShapeStyleMigrationOnFreshDatabaseOnlyWritesMarker(t *testing.T) {
	testutil.SetupTestEnv(t)

	if err := database.DB.Exec("DELETE FROM system_configs WHERE `key` = 'panel_shape_style'").Error; err != nil {
		t.Fatalf("delete row: %v", err)
	}
	database.EnsureColumns()
	if countPanelShapeMarker(t) != 1 {
		t.Fatal("新装实例也应写下迁移标记")
	}
	var rows int64
	database.DB.Raw("SELECT COUNT(*) FROM system_configs WHERE `key` = 'panel_shape_style'").Scan(&rows)
	if rows != 0 {
		t.Fatalf("迁移不应凭空插入 panel_shape_style 行，实际 %d 行", rows)
	}

	model.InitDefaultConfigs()
	if got := readPanelShapeStyleRaw(t); got != "theme" {
		t.Fatalf("新装实例应按注册表默认建成 theme，实际 %q", got)
	}
}
