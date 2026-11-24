/**
 * 侧栏菜单配置（v4.0，design §3）。MainLayout 的侧栏 / 移动端抽屉与命令面板（CommandPalette）共用这一份。
 *
 * 约定（wave1-handoff §8）：
 * - 分组：运行 / 资源 / 其他 / 管理后台。
 * - 15 项图标互不重复（全部来自 @element-plus/icons-vue）。
 * - 「主题工坊」不是路由：action = 'theme-studio'，使用方调 useUiStore().openStudio()。
 * - minRole 沿用 v3.x MainLayout 里的值；路由守卫（router/index.ts 的 meta.minRole）才是真正的权限边界，
 *   这里只决定「看不看得见」。两边改权限时要一起改。
 * - keywords 给命令面板做额外命中（英文别名、常见叫法）；拼音首字母由命令面板自己算，不用写在这里。
 */
import type { Component } from 'vue'
import {
  Avatar,
  Bell,
  Box,
  Brush,
  Collection,
  Cpu,
  Document,
  Download,
  Key,
  Notebook,
  Odometer,
  Operation,
  Setting,
  Tickets,
  Timer,
  User,
} from '@element-plus/icons-vue'

export type PanelRole = 'viewer' | 'operator' | 'admin'

export interface PanelMenuItem {
  key: string
  title: string
  /** 路由路径；有 action 的项没有 path */
  path?: string
  /** 非路由项：点击时由使用方执行对应动作 */
  action?: 'theme-studio'
  icon: Component
  minRole: PanelRole
  keywords?: string[]
}

export interface PanelMenuGroup {
  key: 'run' | 'resource' | 'other' | 'admin'
  title: string
  items: PanelMenuItem[]
}

export const PANEL_MENU_GROUPS: PanelMenuGroup[] = [
  {
    key: 'run',
    title: '运行',
    items: [
      { key: 'dashboard', title: '仪表板', path: '/dashboard', icon: Odometer, minRole: 'viewer', keywords: ['dashboard', 'home', '首页', '概览'] },
      { key: 'tasks', title: '定时任务', path: '/tasks', icon: Timer, minRole: 'viewer', keywords: ['tasks', 'cron', '计划任务'] },
      // 「后台进程」（#176）紧跟在定时任务下面：看的都是任务拉起的进程
      { key: 'processes', title: '后台进程', path: '/processes', icon: Cpu, minRole: 'admin', keywords: ['processes', 'ps', '进程'] },
      { key: 'logs', title: '执行日志', path: '/logs', icon: Tickets, minRole: 'viewer', keywords: ['logs', '日志', '运行记录'] },
    ],
  },
  {
    key: 'resource',
    title: '资源',
    items: [
      { key: 'scripts', title: '脚本管理', path: '/scripts', icon: Document, minRole: 'operator', keywords: ['scripts', '脚本', '文件'] },
      { key: 'subscriptions', title: '订阅管理', path: '/subscriptions', icon: Download, minRole: 'operator', keywords: ['subscriptions', 'repo', '仓库', '拉库'] },
      { key: 'envs', title: '环境变量', path: '/envs', icon: Collection, minRole: 'operator', keywords: ['envs', 'env', '变量'] },
      { key: 'config-file', title: '配置文件', path: '/config-file', icon: Operation, minRole: 'admin', keywords: ['config', 'config.sh', '配置'] },
      { key: 'deps', title: '依赖管理', path: '/deps', icon: Box, minRole: 'admin', keywords: ['deps', 'dependencies', 'pip', 'npm', '依赖'] },
    ],
  },
  {
    key: 'other',
    title: '其他',
    items: [
      { key: 'theme-studio', title: '主题工坊', action: 'theme-studio', icon: Brush, minRole: 'viewer', keywords: ['theme', 'studio', '主题', '外观', '换肤'] },
      { key: 'api-docs', title: '接口文档', path: '/docs/api', icon: Notebook, minRole: 'viewer', keywords: ['api', 'docs', '文档'] },
      { key: 'profile', title: '个人设置', path: '/profile', icon: User, minRole: 'viewer', keywords: ['profile', 'account', '账号', '密码'] },
    ],
  },
  {
    key: 'admin',
    title: '管理后台',
    items: [
      { key: 'admin-settings', title: '系统设置', path: '/admin/settings', icon: Setting, minRole: 'admin', keywords: ['settings', 'system', '系统', '升级', '备份'] },
      { key: 'admin-notifications', title: '通知渠道', path: '/admin/notifications', icon: Bell, minRole: 'admin', keywords: ['notifications', 'notify', '推送'] },
      { key: 'admin-users', title: '用户管理', path: '/admin/users', icon: Avatar, minRole: 'admin', keywords: ['users', '账号', '成员'] },
      { key: 'admin-open-api', title: 'Open API', path: '/admin/open-api', icon: Key, minRole: 'admin', keywords: ['openapi', 'token', '令牌', '应用'] },
    ],
  },
]

/**
 * F9「g 后接一个字母」跳页（design §5 F9 行）：字母 → 菜单项 key。
 * MainLayout 的全局键盘处理与 ShortcutHelpDialog 共用这一份，改字母只改这里。
 */
export const GOTO_SHORTCUTS: { key: string; itemKey: string }[] = [
  { key: 'd', itemKey: 'dashboard' },
  { key: 't', itemKey: 'tasks' },
  { key: 'l', itemKey: 'logs' },
  { key: 's', itemKey: 'scripts' },
  { key: 'e', itemKey: 'envs' },
  { key: 'u', itemKey: 'subscriptions' },
  { key: 'p', itemKey: 'deps' },
]

/** 按 key 找菜单项（跨分组） */
export function findMenuItem(itemKey: string): PanelMenuItem | undefined {
  for (const group of PANEL_MENU_GROUPS) {
    const found = group.items.find((item) => item.key === itemKey)
    if (found) return found
  }
  return undefined
}

const ROLE_LEVEL: Record<string, number> = {
  viewer: 1,
  operator: 2,
  admin: 3,
}

/** 当前角色是否达到 minRole；没登录 / 未知角色一律 false（与 router 里的 hasRequiredRole 同口径） */
export function hasMinRole(role: string | undefined, minRole: PanelRole): boolean {
  if (!role) return false
  return (ROLE_LEVEL[role] || 0) >= (ROLE_LEVEL[minRole] || 0)
}
