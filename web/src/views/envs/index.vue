<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { envApi } from '@/api/env'
import { ElMessage, ElMessageBox } from 'element-plus'

const envList = ref<any[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')
const currentGroup = ref('')
const groups = ref<string[]>([])
const selectedIds = ref<number[]>([])

const showEditDialog = ref(false)
const editForm = ref({ id: 0, name: '', value: '', remarks: '', group: '' })
const isCreate = ref(true)

const showBatchDialog = ref(false)
const batchText = ref('')

const showImportDialog = ref(false)
const importText = ref('')
const importMode = ref('merge')

const showExportDialog = ref(false)
const exportFormat = ref('shell')
const exportContent = ref('')

async function loadData() {
  loading.value = true
  try {
    const res = await envApi.list({
      keyword: keyword.value || undefined,
      group: currentGroup.value || undefined,
      page: page.value,
      page_size: pageSize.value
    })
    envList.value = res.data || []
    total.value = res.total || 0
  } catch {
    ElMessage.error('加载环境变量失败')
  } finally {
    loading.value = false
  }
}

async function loadGroups() {
  try {
    const res = await envApi.groups()
    groups.value = res.data || []
  } catch { /* ignore */ }
}

onMounted(() => {
  loadData()
  loadGroups()
})

function handleSearch() {
  page.value = 1
  loadData()
}

function handleGroupFilter(group: string) {
  currentGroup.value = currentGroup.value === group ? '' : group
  page.value = 1
  loadData()
}

function openCreate() {
  isCreate.value = true
  editForm.value = { id: 0, name: '', value: '', remarks: '', group: '' }
  showEditDialog.value = true
}

function openEdit(row: any) {
  isCreate.value = false
  editForm.value = { id: row.id, name: row.name, value: row.value, remarks: row.remarks, group: row.group }
  showEditDialog.value = true
}

async function handleSave() {
  if (!editForm.value.name.trim()) {
    ElMessage.warning('变量名不能为空')
    return
  }
  try {
    if (isCreate.value) {
      await envApi.create(editForm.value)
      ElMessage.success('创建成功')
    } else {
      await envApi.update(editForm.value.id, {
        name: editForm.value.name,
        value: editForm.value.value,
        remarks: editForm.value.remarks,
        group: editForm.value.group
      })
      ElMessage.success('更新成功')
    }
    showEditDialog.value = false
    loadData()
    loadGroups()
  } catch {
    ElMessage.error(isCreate.value ? '创建失败' : '更新失败')
  }
}

async function handleBatchCreate() {
  const text = batchText.value.trim()
  if (!text) {
    ElMessage.warning('请输入环境变量')
    return
  }
  const lines = text.split('\n').filter(l => l.trim())
  const items: { name: string; value: string }[] = []
  for (const line of lines) {
    const eqIndex = line.indexOf('=')
    if (eqIndex <= 0) {
      ElMessage.warning(`格式错误: ${line}，应为 NAME=VALUE`)
      return
    }
    items.push({
      name: line.substring(0, eqIndex).trim(),
      value: line.substring(eqIndex + 1).trim()
    })
  }
  try {
    await envApi.create(items as any)
    ElMessage.success(`批量创建 ${items.length} 个变量成功`)
    showBatchDialog.value = false
    batchText.value = ''
    loadData()
    loadGroups()
  } catch {
    ElMessage.error('批量创建失败')
  }
}

async function handleDelete(id: number) {
  try {
    await ElMessageBox.confirm('确定要删除该环境变量吗？', '确认删除', { type: 'warning' })
    await envApi.delete(id)
    ElMessage.success('删除成功')
    loadData()
  } catch { /* cancelled */ }
}

async function handleToggle(row: any) {
  try {
    if (row.enabled) {
      await envApi.disable(row.id)
    } else {
      await envApi.enable(row.id)
    }
    loadData()
  } catch {
    ElMessage.error('操作失败')
  }
}

async function handleBatchDelete() {
  if (selectedIds.value.length === 0) return
  try {
    await ElMessageBox.confirm(`确定要删除选中的 ${selectedIds.value.length} 个环境变量吗？`, '批量删除', { type: 'warning' })
    await envApi.batchDelete(selectedIds.value)
    ElMessage.success('批量删除成功')
    selectedIds.value = []
    loadData()
  } catch { /* cancelled */ }
}

async function handleBatchEnable() {
  if (selectedIds.value.length === 0) return
  try {
    await envApi.batchEnable(selectedIds.value)
    ElMessage.success('批量启用成功')
    loadData()
  } catch {
    ElMessage.error('批量启用失败')
  }
}

async function handleBatchDisable() {
  if (selectedIds.value.length === 0) return
  try {
    await envApi.batchDisable(selectedIds.value)
    ElMessage.success('批量禁用成功')
    loadData()
  } catch {
    ElMessage.error('批量禁用失败')
  }
}

function handleSelectionChange(rows: any[]) {
  selectedIds.value = rows.map(r => r.id)
}

async function handleImport() {
  let envs: any[]
  try {
    envs = JSON.parse(importText.value)
    if (!Array.isArray(envs)) throw new Error()
  } catch {
    ElMessage.error('JSON 格式不正确，需要数组格式')
    return
  }
  try {
    const res = await envApi.import(envs, importMode.value)
    ElMessage.success(res.message)
    showImportDialog.value = false
    importText.value = ''
    loadData()
    loadGroups()
  } catch {
    ElMessage.error('导入失败')
  }
}

function handleImportFile(file: File) {
  const reader = new FileReader()
  reader.onload = (e) => {
    const text = e.target?.result as string
    try {
      const parsed = JSON.parse(text)
      importText.value = JSON.stringify(parsed, null, 2)
    } catch {
      importText.value = text
      ElMessage.warning('文件内容不是有效的 JSON，已填入原始内容')
    }
  }
  reader.readAsText(file)
  return false
}

async function handleExportAll() {
  try {
    const res = await envApi.exportAll()
    const json = JSON.stringify(res.data, null, 2)
    const blob = new Blob([json], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'env_vars.json'
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    ElMessage.error('导出失败')
  }
}

async function handleExportFiles() {
  showExportDialog.value = true
  try {
    const res = await envApi.exportFiles(exportFormat.value, true)
    exportContent.value = res.data[exportFormat.value] || ''
  } catch {
    ElMessage.error('导出失败')
  }
}

async function refreshExport() {
  try {
    const res = await envApi.exportFiles(exportFormat.value, true)
    exportContent.value = res.data[exportFormat.value] || ''
  } catch { /* ignore */ }
}

function copyExport() {
  navigator.clipboard.writeText(exportContent.value)
  ElMessage.success('已复制到剪贴板')
}
</script>

<template>
  <div class="envs-page">
    <div class="page-header">
      <div class="header-left">
        <el-input
          v-model="keyword"
          placeholder="搜索变量名或备注"
          clearable
          style="width: 240px"
          @keyup.enter="handleSearch"
          @clear="handleSearch"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <div class="group-tags" v-if="groups.length > 0">
          <el-tag
            v-for="g in groups"
            :key="g"
            :effect="currentGroup === g ? 'dark' : 'plain'"
            class="group-tag"
            @click="handleGroupFilter(g)"
          >
            {{ g }}
          </el-tag>
        </div>
      </div>
      <div class="header-right">
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>新建
        </el-button>
        <el-button @click="showBatchDialog = true">
          <el-icon><DocumentAdd /></el-icon>批量添加
        </el-button>
        <el-button @click="handleBatchEnable" :disabled="selectedIds.length === 0">
          <el-icon><Check /></el-icon>批量启用
        </el-button>
        <el-button @click="handleBatchDisable" :disabled="selectedIds.length === 0">
          <el-icon><Close /></el-icon>批量禁用
        </el-button>
        <el-button @click="handleBatchDelete" :disabled="selectedIds.length === 0">
          <el-icon><Delete /></el-icon>批量删除
        </el-button>
        <el-dropdown trigger="click">
          <el-button>
            <el-icon><Download /></el-icon>导出
          </el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item @click="handleExportAll">导出 JSON</el-dropdown-item>
              <el-dropdown-item @click="exportFormat = 'shell'; handleExportFiles()">导出 Shell</el-dropdown-item>
              <el-dropdown-item @click="exportFormat = 'js'; handleExportFiles()">导出 JS</el-dropdown-item>
              <el-dropdown-item @click="exportFormat = 'python'; handleExportFiles()">导出 Python</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button @click="showImportDialog = true">
          <el-icon><Upload /></el-icon>导入
        </el-button>
      </div>
    </div>

    <el-table
      :data="envList"
      v-loading="loading"
      @selection-change="handleSelectionChange"
      stripe
      class="env-table"
    >
      <el-table-column type="selection" width="50" />
      <el-table-column prop="name" label="变量名" min-width="160">
        <template #default="{ row }">
          <span class="env-name">{{ row.name }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="value" label="值" min-width="200" show-overflow-tooltip />
      <el-table-column prop="remarks" label="备注" min-width="150" show-overflow-tooltip />
      <el-table-column prop="group" label="分组" width="120">
        <template #default="{ row }">
          <el-tag v-if="row.group" size="small" effect="plain">{{ row.group }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="80" align="center">
        <template #default="{ row }">
          <el-switch
            :model-value="row.enabled"
            size="small"
            @change="handleToggle(row)"
          />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" text type="danger" @click="handleDelete(row.id)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-container" v-if="total > pageSize">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        @current-change="loadData"
        @size-change="() => { page = 1; loadData() }"
      />
    </div>

    <el-dialog v-model="showEditDialog" :title="isCreate ? '新建环境变量' : '编辑环境变量'" width="500px">
      <el-form :model="editForm" label-width="80px">
        <el-form-item label="变量名">
          <el-input v-model="editForm.name" placeholder="变量名 (如: API_KEY)" />
        </el-form-item>
        <el-form-item label="值">
          <el-input v-model="editForm.value" type="textarea" :rows="3" placeholder="变量值" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="editForm.remarks" placeholder="备注说明" />
        </el-form-item>
        <el-form-item label="分组">
          <el-input v-model="editForm.group" placeholder="分组 (可选)" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showEditDialog = false">取消</el-button>
        <el-button type="primary" @click="handleSave">{{ isCreate ? '创建' : '保存' }}</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showImportDialog" title="导入环境变量" width="600px">
      <el-form label-width="80px">
        <el-form-item label="导入模式">
          <el-radio-group v-model="importMode">
            <el-radio value="merge">合并 (同名同值更新)</el-radio>
            <el-radio value="replace">替换 (清空后导入)</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="JSON 数据">
          <div style="width: 100%">
            <el-upload
              :show-file-list="false"
              :before-upload="handleImportFile"
              accept=".json"
              style="margin-bottom: 8px"
            >
              <el-button size="small"><el-icon><Upload /></el-icon>选择 JSON 文件</el-button>
            </el-upload>
            <el-input
              v-model="importText"
              type="textarea"
              :rows="10"
              placeholder='[{"name": "KEY", "value": "VALUE", "remarks": "", "group": ""}]'
            />
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showImportDialog = false">取消</el-button>
        <el-button type="primary" @click="handleImport">导入</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showBatchDialog" title="批量添加环境变量" width="550px">
      <el-alert type="info" :closable="false" style="margin-bottom: 12px">
        每行一个变量，格式: NAME=VALUE
      </el-alert>
      <el-input
        v-model="batchText"
        type="textarea"
        :rows="10"
        placeholder="API_KEY=your_key&#10;SECRET=your_secret&#10;TOKEN=your_token"
      />
      <template #footer>
        <el-button @click="showBatchDialog = false">取消</el-button>
        <el-button type="primary" @click="handleBatchCreate">批量创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showExportDialog" title="导出环境变量" width="600px">
      <div class="export-format-switch">
        <el-radio-group v-model="exportFormat" @change="refreshExport">
          <el-radio-button value="shell">Shell</el-radio-button>
          <el-radio-button value="js">JavaScript</el-radio-button>
          <el-radio-button value="python">Python</el-radio-button>
        </el-radio-group>
        <el-button size="small" @click="copyExport">
          <el-icon><CopyDocument /></el-icon>复制
        </el-button>
      </div>
      <pre class="export-preview">{{ exportContent }}</pre>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.envs-page {
  padding: 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
  flex-wrap: wrap;
  gap: 12px;

  .header-left {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
  }

  .header-right {
    display: flex;
    align-items: center;
    gap: 8px;
  }
}

.group-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;

  .group-tag {
    cursor: pointer;
    &:hover { opacity: 0.8; }
  }
}

.env-name {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-color-primary);
}

.env-table {
  width: 100%;
}

.pagination-container {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}

.export-format-switch {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.export-preview {
  background: var(--el-bg-color-page);
  border-radius: 6px;
  padding: 16px;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
  max-height: 400px;
  overflow-y: auto;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
