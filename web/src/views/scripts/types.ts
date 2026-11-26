export interface TreeNode {
  title: string
  key: string
  isLeaf: boolean
  children?: TreeNode[]
  /** 修改时间（Unix 秒） */
  mtime?: number
  /** 创建时间（Unix 秒），系统拿不到时后端不带，排序时按 mtime 兜底 */
  btime?: number
  /** 懒加载目录（#183）：子项太多没一起返回，children 为空，展开时再用 GET /scripts/tree?lazy=1&path= 去取 */
  lazy?: boolean
  /** 前端给懒加载目录塞的占位子节点（只为让 el-tree 显示展开箭头），不是真文件 */
  placeholder?: boolean
}

export interface ScriptVersionRecord {
  id: number
  version: number | string
  message: string
  content_length: number
  created_at: string
}

export interface ScriptVersionDetail extends ScriptVersionRecord {
  script_path: string
  content: string
}
