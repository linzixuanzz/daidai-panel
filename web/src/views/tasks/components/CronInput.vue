<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { taskApi } from '@/api/task'

const props = defineProps<{
  modelValue: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const expression = ref(props.modelValue || '* * * * *')
const parseResult = ref<any>(null)
const templates = ref<any[]>([])
const showAllTemplates = ref(false)

watch(() => props.modelValue, (val) => {
  expression.value = val
})

watch(expression, async (val) => {
  emit('update:modelValue', val)
  if (val) {
    try {
      parseResult.value = await taskApi.cronParse(val)
    } catch {
      parseResult.value = null
    }
  }
}, { immediate: true })

async function loadTemplates() {
  if (templates.value.length === 0) {
    try {
      const res = await taskApi.cronTemplates()
      templates.value = res || []
    } catch {
      templates.value = []
    }
  }
}

loadTemplates()

function selectTemplate(tmpl: any) {
  expression.value = tmpl.expression
}

function handleKeyDown(e: KeyboardEvent) {
  if (e.key === ' ') {
    e.stopPropagation()
  }
}

const commonTemplates = computed(() => {
  return templates.value.filter(t => t.category === '常用').slice(0, 2)
})

const groupedTemplates = computed(() => {
  const groups: Record<string, any[]> = {}
  for (const t of templates.value) {
    if (!groups[t.category]) groups[t.category] = []
    groups[t.category]!.push(t)
  }
  return groups
})
</script>

<template>
  <div class="cron-input">
    <el-input
      v-model="expression"
      placeholder="cron 表达式 (秒 分 时 日 月 周, 如: 0 */5 * * * *)"
      clearable
      @keydown="handleKeyDown"
    >
      <template #append>
        <el-button @click="showAllTemplates = true">
          <el-icon><Clock /></el-icon>
        </el-button>
      </template>
    </el-input>

    <div v-if="parseResult" class="cron-info">
      <template v-if="parseResult.is_valid">
        <div class="valid-badge">
          <el-icon class="badge-icon"><CircleCheck /></el-icon>
          <span class="badge-text">{{ parseResult.description }}</span>
        </div>
        <div v-if="parseResult.next_run_times?.length" class="next-times">
          <el-icon class="time-icon"><Clock /></el-icon>
          <span class="label">下次执行</span>
          <span class="time-value">{{ new Date(parseResult.next_run_times[0]).toLocaleString() }}</span>
        </div>
      </template>
      <div v-else class="error-badge">
        <el-icon class="badge-icon"><CircleClose /></el-icon>
        <span class="badge-text">{{ parseResult.error }}</span>
      </div>
    </div>

    <div v-if="commonTemplates.length > 0" class="common-templates">
      <div class="templates-header">
        <el-icon class="header-icon"><Timer /></el-icon>
        <span class="templates-label">常用规则</span>
      </div>
      <div class="templates-list">
        <div
          v-for="t in commonTemplates"
          :key="t.expression"
          class="template-card"
          :class="{ active: expression === t.expression }"
          @click="selectTemplate(t)"
        >
          <div class="card-name">{{ t.name }}</div>
          <div class="card-expr">{{ t.expression }}</div>
        </div>
        <div class="template-card more-card" @click="showAllTemplates = true">
          <el-icon class="more-icon"><More /></el-icon>
          <div class="card-name">更多规则</div>
        </div>
      </div>
    </div>

    <el-dialog
      v-model="showAllTemplates"
      title="选择定时规则"
      width="700px"
      :close-on-click-modal="false"
    >
      <div class="cron-templates-dialog">
        <div v-for="(items, category) in groupedTemplates" :key="category" class="template-group">
          <div class="group-header">
            <div class="group-title">{{ category }}</div>
            <div class="group-count">{{ items.length }} 个规则</div>
          </div>
          <div class="group-items">
            <div
              v-for="t in items"
              :key="t.expression"
              class="template-item"
              :class="{ active: expression === t.expression }"
              @click="selectTemplate(t); showAllTemplates = false"
            >
              <div class="item-header">
                <span class="item-name">{{ t.name }}</span>
                <el-icon v-if="expression === t.expression" class="check-icon"><Check /></el-icon>
              </div>
              <div class="item-expr">{{ t.expression }}</div>
            </div>
          </div>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.cron-input {
  width: 100%;
}

.cron-info {
  margin-top: 12px;
  display: flex;
  align-items: center;
  gap: 16px;
  font-size: 13px;

  .valid-badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 10px;
    background: linear-gradient(135deg, #67c23a 0%, #85ce61 100%);
    border-radius: 14px;
    color: #fff;
    font-weight: 500;
    box-shadow: 0 2px 6px rgba(103, 194, 58, 0.25);
    transition: all 0.3s var(--ease-smooth);
    position: relative;
    overflow: hidden;

    &::before {
      content: '';
      position: absolute;
      top: 0;
      left: -100%;
      width: 100%;
      height: 100%;
      background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.3), transparent);
      transition: left 0.5s;
    }

    &:hover::before {
      left: 100%;
    }

    .badge-icon {
      font-size: 14px;
      animation: successPulse 2s ease-in-out infinite;
    }

    .badge-text {
      font-size: 12px;
    }
  }

  .error-badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 10px;
    background: linear-gradient(135deg, #f56c6c 0%, #f78989 100%);
    border-radius: 14px;
    color: #fff;
    font-weight: 500;
    box-shadow: 0 2px 6px rgba(245, 108, 108, 0.25);
    transition: all 0.3s var(--ease-smooth);

    .badge-icon {
      font-size: 14px;
      animation: errorShake 0.5s ease-in-out;
    }

    .badge-text {
      font-size: 12px;
    }
  }

  .next-times {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 4px 10px;
    background: linear-gradient(135deg, var(--el-color-primary-light-9) 0%, var(--el-color-primary-light-8) 100%);
    border-radius: 14px;
    color: var(--el-color-primary);
    font-weight: 500;
    border: 1px solid var(--el-color-primary-light-7);
    transition: all 0.3s var(--ease-smooth);

    .time-icon {
      font-size: 13px;
    }

    .label {
      font-size: 11px;
    }

    .time-value {
      font-family: var(--dd-font-mono);
      font-size: 11px;
    }

    &:hover {
      transform: translateY(-1px);
      box-shadow: 0 2px 8px rgba(64, 158, 255, 0.2);
      border-color: var(--el-color-primary-light-5);
    }
  }
}

.common-templates {
  margin-top: 16px;
  padding: 16px;
  background: linear-gradient(135deg, var(--el-fill-color-lighter) 0%, var(--el-fill-color-light) 100%);
  border-radius: 8px;
  border: 1px solid var(--el-border-color-lighter);

  .templates-header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 12px;

    .header-icon {
      font-size: 16px;
      color: var(--el-color-primary);
    }

    .templates-label {
      font-size: 13px;
      font-weight: 600;
      color: var(--el-text-color-primary);
    }
  }

  .templates-list {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 10px;
  }

  .template-card {
    padding: 12px;
    background: var(--el-bg-color);
    border: 2px solid var(--el-border-color-light);
    border-radius: 8px;
    cursor: pointer;
    transition: all 0.3s var(--ease-smooth);
    position: relative;
    overflow: hidden;

    &::before {
      content: '';
      position: absolute;
      top: 0;
      left: 0;
      right: 0;
      height: 3px;
      background: linear-gradient(90deg, var(--el-color-primary), var(--el-color-primary-light-3));
      transform: scaleX(0);
      transform-origin: left;
      transition: transform 0.3s var(--ease-smooth);
    }

    .card-name {
      font-size: 13px;
      font-weight: 600;
      color: var(--el-text-color-primary);
      margin-bottom: 6px;
    }

    .card-expr {
      font-size: 11px;
      color: var(--el-text-color-secondary);
      font-family: var(--dd-font-mono);
      background: var(--el-fill-color-light);
      padding: 4px 6px;
      border-radius: 4px;
    }

    &:hover {
      transform: translateY(-2px) scale3d(1.02, 1.02, 1);
      border-color: var(--el-color-primary-light-5);
      box-shadow: 0 4px 12px rgba(64, 158, 255, 0.15);

      &::before {
        transform: scaleX(1);
      }
    }

    &.active {
      border-color: var(--el-color-primary);
      background: linear-gradient(135deg, var(--el-color-primary-light-9) 0%, var(--el-bg-color) 100%);

      &::before {
        transform: scaleX(1);
      }

      .card-name {
        color: var(--el-color-primary);
      }
    }

    &.more-card {
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      gap: 6px;
      border-style: dashed;
      border-color: var(--el-color-primary-light-5);

      .more-icon {
        font-size: 20px;
        color: var(--el-color-primary);
      }

      .card-name {
        margin: 0;
        color: var(--el-color-primary);
      }

      &:hover {
        border-color: var(--el-color-primary);
        background: var(--el-color-primary-light-9);
      }
    }
  }
}

.cron-templates-dialog {
  max-height: 65vh;
  overflow-y: auto;
  padding: 4px;

  .template-group {
    margin-bottom: 24px;
    &:last-child { margin-bottom: 0; }

    .group-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 12px;
      padding: 10px 12px;
      background: linear-gradient(135deg, var(--el-fill-color-light) 0%, var(--el-fill-color) 100%);
      border-radius: 6px;
      border-left: 4px solid var(--el-color-primary);

      .group-title {
        font-size: 14px;
        font-weight: 600;
        color: var(--el-text-color-primary);
      }

      .group-count {
        font-size: 12px;
        color: var(--el-text-color-secondary);
        background: var(--el-bg-color);
        padding: 2px 8px;
        border-radius: 10px;
      }
    }

    .group-items {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
      gap: 12px;
    }

    .template-item {
      padding: 14px;
      background: var(--el-bg-color);
      border: 2px solid var(--el-border-color-light);
      border-radius: 8px;
      cursor: pointer;
      transition: all 0.3s var(--ease-smooth);
      position: relative;

      &::before {
        content: '';
        position: absolute;
        top: 0;
        left: 0;
        right: 0;
        height: 3px;
        background: linear-gradient(90deg, var(--el-color-primary), var(--el-color-success));
        transform: scaleX(0);
        transform-origin: left;
        transition: transform 0.3s var(--ease-smooth);
      }

      .item-header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        margin-bottom: 8px;

        .item-name {
          font-size: 14px;
          font-weight: 600;
          color: var(--el-text-color-primary);
        }

        .check-icon {
          font-size: 16px;
          color: var(--el-color-success);
          animation: checkBounce 0.4s var(--ease-smooth);
        }
      }

      .item-expr {
        font-size: 12px;
        color: var(--el-text-color-secondary);
        font-family: var(--dd-font-mono);
        background: var(--el-fill-color-light);
        padding: 6px 8px;
        border-radius: 4px;
        word-break: break-all;
      }

      &:hover {
        transform: translateY(-2px) scale3d(1.02, 1.02, 1);
        border-color: var(--el-color-primary-light-5);
        box-shadow: 0 6px 16px rgba(64, 158, 255, 0.2);

        &::before {
          transform: scaleX(1);
        }
      }

      &.active {
        border-color: var(--el-color-success);
        background: linear-gradient(135deg, var(--el-color-success-light-9) 0%, var(--el-bg-color) 100%);

        &::before {
          transform: scaleX(1);
          background: linear-gradient(90deg, var(--el-color-success), var(--el-color-success-light-3));
        }

        .item-header .item-name {
          color: var(--el-color-success);
        }
      }
    }
  }
}

@keyframes checkBounce {
  0% {
    transform: scale3d(0, 0, 1);
    opacity: 0;
  }
  50% {
    transform: scale3d(1.2, 1.2, 1);
  }
  100% {
    transform: scale3d(1, 1, 1);
    opacity: 1;
  }
}

@keyframes successPulse {
  0%, 100% {
    transform: scale3d(1, 1, 1);
  }
  50% {
    transform: scale3d(1.1, 1.1, 1);
  }
}

@keyframes errorShake {
  0%, 100% {
    transform: translateX(0);
  }
  25% {
    transform: translateX(-4px);
  }
  75% {
    transform: translateX(4px);
  }
}
</style>
