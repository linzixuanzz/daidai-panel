<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { SponsorRecord, SponsorSummary } from '@/api/sponsor'

const props = defineProps<{
  sponsors: SponsorRecord[]
  summary: SponsorSummary | null
  loading: boolean
}>()

const avatarRetryDelayMs = 1800
const avatarRetryLimit = 2
const brokenAvatarKeys = ref<string[]>([])
const avatarRetryAttempts = ref<Record<string, number>>({})
const avatarRetryTimers = new Map<string, ReturnType<typeof setTimeout>>()

function sponsorAvatarKey(sponsor: Pick<SponsorRecord, 'id' | 'avatar_url'>) {
  return `${sponsor.id}:${sponsor.avatar_url || ''}`
}

function clearAvatarRetryTimer(key: string) {
  const timer = avatarRetryTimers.get(key)
  if (timer) {
    clearTimeout(timer)
    avatarRetryTimers.delete(key)
  }
}

watch(
  () => props.sponsors.map(sponsorAvatarKey),
  (keys) => {
    const activeKeys = new Set(keys)
    brokenAvatarKeys.value = brokenAvatarKeys.value.filter((key) => activeKeys.has(key))
    avatarRetryAttempts.value = Object.fromEntries(
      Object.entries(avatarRetryAttempts.value).filter(([key]) => activeKeys.has(key))
    )

    for (const key of Array.from(avatarRetryTimers.keys())) {
      if (!activeKeys.has(key)) {
        clearAvatarRetryTimer(key)
      }
    }
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  for (const key of Array.from(avatarRetryTimers.keys())) {
    clearAvatarRetryTimer(key)
  }
})

const sortedSponsors = computed(() => {
  return [...props.sponsors].sort((left, right) => {
    const amountDiff = (Number(right.amount) || 0) - (Number(left.amount) || 0)
    if (amountDiff !== 0) return amountDiff

    const rightTime = right.updated_at ? new Date(right.updated_at).getTime() : 0
    const leftTime = left.updated_at ? new Date(left.updated_at).getTime() : 0
    return rightTime - leftTime
  })
})

type PodiumSlot = 'first' | 'second' | 'third'

interface PodiumEntry {
  slot: PodiumSlot
  rank: 1 | 2 | 3
  sponsor: SponsorRecord | null
}

const podiumSponsors = computed<PodiumEntry[]>(() => {
  const [first, second, third] = sortedSponsors.value
  return [
    { slot: 'first', rank: 1, sponsor: first || null },
    { slot: 'second', rank: 2, sponsor: second || null },
    { slot: 'third', rank: 3, sponsor: third || null },
  ]
})

const remainingSponsors = computed(() => sortedSponsors.value.slice(3))

const sponsorServiceUnavailable = computed(() => !!props.summary?.unavailable)

function markAvatarBroken(sponsor: SponsorRecord) {
  const key = sponsorAvatarKey(sponsor)
  const attempt = (avatarRetryAttempts.value[key] || 0) + 1
  avatarRetryAttempts.value = {
    ...avatarRetryAttempts.value,
    [key]: attempt,
  }

  if (!brokenAvatarKeys.value.includes(key)) {
    brokenAvatarKeys.value = [...brokenAvatarKeys.value, key]
  }

  if (attempt > avatarRetryLimit) {
    clearAvatarRetryTimer(key)
    return
  }

  clearAvatarRetryTimer(key)
  avatarRetryTimers.set(key, setTimeout(() => {
    clearAvatarRetryTimer(key)
    brokenAvatarKeys.value = brokenAvatarKeys.value.filter((item) => item !== key)
  }, avatarRetryDelayMs * attempt))
}

function isAvatarBroken(sponsor: SponsorRecord) {
  return brokenAvatarKeys.value.includes(sponsorAvatarKey(sponsor))
}

function formatAmount(amount: number) {
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: 'CNY',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(amount || 0)
}

function rankLabel(rank: number) {
  if (rank === 1) return '第一名'
  if (rank === 2) return '第二名'
  return '第三名'
}
</script>

<template>
  <el-card shadow="never" class="sponsor-wall">
    <template #header>
      <div class="sponsor-wall__header">
        <div class="card-title-bar sponsor-wall__header-copy">
          <span class="title-dot"></span>
          <span class="title-text">赞助名单</span>
        </div>
        <div class="sponsor-wall__summary" v-if="summary">
          <span class="summary-pill">{{ summary.count }} 位支持者</span>
          <span class="summary-pill">{{ formatAmount(summary.total_amount) }}</span>
        </div>
      </div>
    </template>

    <div v-if="loading && sponsors.length === 0" class="sponsor-loading">
      <span v-for="item in 6" :key="item" class="sponsor-loading__card"></span>
    </div>

    <div v-else-if="sponsors.length === 0" class="sponsor-empty">
      <h4>{{ sponsorServiceUnavailable ? '赞助名单服务暂时不可用' : '还没有上墙的赞助人' }}</h4>
      <p>
        {{
          sponsorServiceUnavailable
            ? '当前页面会在后台自动重试拉取。'
            : '录入姓名、头像和金额后，这里会自动展示最新赞助名单。'
        }}
      </p>
    </div>

    <div v-else class="sponsor-layout">
      <section class="sponsor-podium" aria-label="赞助金额前三名">
        <div
          v-for="entry in podiumSponsors"
          :key="entry.slot"
          class="sponsor-podium__slot"
          :class="`sponsor-podium__slot--${entry.slot}`"
        >
          <template v-if="entry.sponsor">
            <article class="podium-card" :class="`podium-card--${entry.slot}`">
              <span class="podium-card__rank">{{ rankLabel(entry.rank) }}</span>
              <div class="podium-card__avatar">
                <img
                  v-if="entry.sponsor.avatar_url && !isAvatarBroken(entry.sponsor)"
                  :src="entry.sponsor.avatar_url"
                  :alt="entry.sponsor.name"
                  referrerpolicy="no-referrer"
                  @error="markAvatarBroken(entry.sponsor)"
                />
                <span v-else>{{ entry.sponsor.initial }}</span>
              </div>
              <div class="podium-card__name">{{ entry.sponsor.name }}</div>
              <div class="podium-card__amount">{{ formatAmount(entry.sponsor.amount) }}</div>
            </article>
            <div class="podium-base" :class="`podium-base--${entry.slot}`"></div>
          </template>
          <div v-else class="sponsor-podium__placeholder" aria-hidden="true"></div>
        </div>
      </section>

      <section v-if="remainingSponsors.length > 0" class="sponsor-grid">
        <article
          v-for="sponsor in remainingSponsors"
          :key="sponsor.id"
          class="sponsor-card"
        >
          <div class="sponsor-card__avatar">
            <img
              v-if="sponsor.avatar_url && !isAvatarBroken(sponsor)"
              :src="sponsor.avatar_url"
              :alt="sponsor.name"
              referrerpolicy="no-referrer"
              @error="markAvatarBroken(sponsor)"
            />
            <span v-else>{{ sponsor.initial }}</span>
          </div>
          <div class="sponsor-card__body">
            <div class="sponsor-card__name">{{ sponsor.name }}</div>
          </div>
          <div class="sponsor-card__amount">{{ formatAmount(sponsor.amount) }}</div>
        </article>
      </section>
    </div>
  </el-card>
</template>

<style scoped lang="scss">
/* 赞助墙：淡琥珀底 + 描边，去掉两层圆形光晕与底色渐变。
   v4.0 起全部读主题令牌：琥珀取警告色 --dd-warn（各主题都有亮 / 暗两版），文字用墨色 --dd-warn-ink，
   卡片底用实色 --dd-surface-solid。明暗两套都由令牌给出，不再需要 html.dark 分支
   （global.scss 里原来那层写死深蓝灰的暗色覆盖第一波已删）。

   模板上的 el-card 必须用 shadow="never"（与全站其余 el-card 保持一致）。
   一旦写成 shadow="hover"/"always"，就会命中 EP 自带的 `.el-card.is-hover-shadow:hover`
   / `.el-card.is-always-shadow` 规则重新长出悬浮阴影，与 R3-a「去阴影」冲突；
   这两条 EP 规则特异度是 (0,2,0)，压得过本文件里的 `.sponsor-wall`。 */
.sponsor-wall {
  overflow: hidden;
  background: color-mix(in srgb, var(--dd-warn) 7%, var(--dd-surface-solid));
  border: var(--dd-border-width) solid color-mix(in srgb, var(--dd-warn) 20%, var(--dd-border));
}

.sponsor-wall__header {
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
}

.card-title-bar {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 注意：类名叫 dot，实际是 3×14 的竖条色标（不是状态灯），所以不进圆形白名单。

   归档为「装饰性细指示条」→ pill 档，与 api-docs 的 .api-card 品牌色竖条、
   ScriptsSidebar 当前项的左缘条同类，全站这一类统一走 pill。
   ⚠️ 这里换档是【为了归类一致，不是为了改观感】：3px 宽的盒子上，CSS 圆角等比收缩会把
   control(6px) 和 pill(999px) 一起夹到 1.5px（= 半个宽度），两者渲染结果完全相同。
   写 pill 是为了让「细条 = 胶囊」这条判据在代码里读得出来，避免后人以为它属于控件类。 */
.title-dot {
  width: 3px;
  height: 14px;
  border-radius: var(--dd-radius-pill);
  background: var(--dd-warn);
  display: inline-block;
  flex-shrink: 0;
}

.title-text {
  font-size: 14px;
  font-weight: 700;
  color: var(--dd-warn-ink);
}

.sponsor-wall__summary {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--dd-text-2);
}

.summary-pill {
  padding: 6px 10px;
  /* 「N 位支持者 / 累计金额」计数角标，天然胶囊 → pill 档 */
  border-radius: var(--dd-radius-pill);
  background: var(--dd-surface-solid);
  border: 1px solid color-mix(in srgb, var(--dd-warn) 16%, var(--dd-border));
}

.sponsor-loading {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(190px, 1fr));
  gap: 12px;
}

/* 骨架屏：占位块；::after 的横向流光是加载指示（非装饰），保留 */
.sponsor-loading__card {
  position: relative;
  overflow: hidden;
  min-height: 88px;
  /* 占位块要和真实的 .sponsor-card 同形，否则加载完会看到形状跳变 → 同取 surface 档 */
  border-radius: var(--dd-radius-surface);
  background: var(--dd-surface-solid);
  border: 1px solid color-mix(in srgb, var(--dd-warn) 18%, var(--dd-border));

  &::after {
    content: '';
    position: absolute;
    inset: 0;
    transform: translateX(-100%);
    // 流光取二级底色：亮色是一道浅灰、暗色是一道略亮的深色，两种明暗下都看得见
    background: linear-gradient(90deg, transparent, var(--dd-surface-2), transparent);
    animation: sponsor-loading 1.4s ease-in-out infinite;
  }
}

@keyframes sponsor-loading {
  to {
    transform: translateX(100%);
  }
}

.sponsor-empty {
  position: relative;
  padding: 28px 24px;
  /* 空状态面板是容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  background: var(--dd-surface-solid);
  border: 1px dashed color-mix(in srgb, var(--dd-warn) 30%, var(--dd-border));
  text-align: center;

  h4 {
    margin: 10px 0 8px;
    font-size: 24px;
    font-weight: 700;
    color: var(--dd-warn-ink);
  }

  p {
    margin: 0 auto;
    max-width: 520px;
    font-size: 13px;
    line-height: 1.8;
    color: var(--dd-text-2);
  }
}

.sponsor-layout {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.sponsor-podium {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  grid-template-areas: 'second first third';
  gap: 12px;
  align-items: end;
}

.sponsor-podium__slot {
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  gap: 10px;
}

.sponsor-podium__slot--first {
  grid-area: first;
}

.sponsor-podium__slot--second {
  grid-area: second;
}

.sponsor-podium__slot--third {
  grid-area: third;
}

.sponsor-podium__placeholder {
  min-height: 1px;
}

.podium-card {
  position: relative;
  min-height: 144px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 18px 16px 16px;
  /* 纯色卡：去掉顶部圆形高光、投影与 hover 上浮，层次交给 1px 描边。卡片本体 → surface 档 */
  border-radius: var(--dd-radius-surface);
  text-align: center;
  background: var(--dd-surface-solid);
  border: 1px solid color-mix(in srgb, var(--dd-warn) 18%, var(--dd-border));
}

/* 第一名：用更暖的琥珀底与其余两名区分（原为渐变） */
.podium-card--first {
  background: color-mix(in srgb, var(--dd-warn) 10%, var(--dd-surface-solid));
}

.podium-card__rank {
  position: absolute;
  top: 12px;
  left: 12px;
  padding: 4px 10px;
  /* 「No.1」名次标签，是卡片里的小标签而非状态胶囊 → control 档（与 .dd-stat-card__delta 同档） */
  border-radius: var(--dd-radius-control);
  background: var(--dd-surface-solid);
  border: 1px solid color-mix(in srgb, var(--dd-warn) 22%, var(--dd-border));
  font-size: 11px;
  font-weight: 700;
  color: var(--dd-warn-ink);
}

.podium-card__avatar {
  width: 54px;
  height: 54px;
  flex-shrink: 0;

  img,
  span {
    width: 100%;
    height: 100%;
    /* 白名单：形状承载语义 —— 这里直接渲染 GitHub 头像，头像本身按圆形构图，
       方切后人像被裁到角上观感最差。两种 shape 模式下都固定圆形，不吃 --dd-radius-* 刻度。
       （底色用实心警告色令牌，配 --dd-on-status 文字，去掉了渐变与投影） */
    border-radius: 50%;
    object-fit: cover;
    background: var(--dd-warn-solid);
    color: var(--dd-on-status);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 20px;
    font-weight: 700;
  }
}

.podium-card--first .podium-card__avatar {
  width: 58px;
  height: 58px;
}

.podium-card__name {
  width: 100%;
  font-size: 15px;
  font-weight: 700;
  color: var(--dd-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.podium-card__amount {
  font-size: 18px;
  font-weight: 800;
  color: var(--dd-warn-ink);
  white-space: nowrap;
}

.podium-card--first .podium-card__amount {
  font-size: 19px;
}

/* 领奖台底座：纯色块，去掉渐变与顶部内高光。它与上方卡片之间有 gap，是独立的容器类表面 → surface 档 */
.podium-base {
  border-radius: var(--dd-radius-surface);
  border: 1px solid color-mix(in srgb, var(--dd-warn) 20%, var(--dd-border));
  background: color-mix(in srgb, var(--dd-warn) 16%, var(--dd-surface-solid));
}

.podium-base--first {
  height: 56px;
}

.podium-base--second {
  height: 42px;
}

.podium-base--third {
  height: 34px;
}

.sponsor-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
}

.sponsor-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  /* 赞助者列表卡片本体 → surface 档 */
  border-radius: var(--dd-radius-surface);
  background: var(--dd-surface-solid);
  border: 1px solid color-mix(in srgb, var(--dd-warn) 18%, var(--dd-border));
}

.sponsor-card__avatar {
  width: 44px;
  height: 44px;
  flex-shrink: 0;

  img,
  span {
    width: 100%;
    height: 100%;
    /* 白名单：形状承载语义 —— 同 .podium-card__avatar，直接渲染 GitHub 头像，固定圆形 */
    border-radius: 50%;
    object-fit: cover;
    background: var(--dd-warn-solid);
    color: var(--dd-on-status);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 18px;
    font-weight: 700;
  }
}

.sponsor-card__body {
  min-width: 0;
  flex: 1;
}

.sponsor-card__name {
  font-size: 14px;
  font-weight: 700;
  color: var(--dd-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sponsor-card__amount {
  font-size: 16px;
  font-weight: 800;
  color: var(--dd-warn-ink);
  white-space: nowrap;
}

@media (max-width: 768px) {
  .sponsor-podium {
    grid-template-columns: 1fr;
    grid-template-areas:
      'first'
      'second'
      'third';
  }

  .sponsor-podium__placeholder {
    display: none;
  }

  .podium-card {
    min-height: 126px;
  }

  .podium-base {
    height: 18px !important;
  }

  .sponsor-grid {
    grid-template-columns: 1fr;
  }

  .sponsor-empty {
    padding: 24px 18px;
  }

  .sponsor-card {
    align-items: flex-start;
  }
}

</style>
