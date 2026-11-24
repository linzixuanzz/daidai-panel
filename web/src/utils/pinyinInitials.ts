/**
 * 拼音首字母（命令面板 F1 用）：不引拼音库，靠浏览器自带的中文拼音排序（Intl.Collator 'zh-Hans-CN'）实现。
 *
 * 原理（「边界字」法）：拼音排序下，所有汉字按拼音字母序排好。只要知道每个声母字母「第一个汉字」是谁，
 * 一个汉字落在哪两个边界字之间，它的首字母就是哪个。I / U / V 没有汉字以它们开头，所以只有 23 个边界。
 * 边界字是在 Node（完整 ICU）里把 U+4E00~U+9FA5 全排一遍后逐个核对前后邻居挑出来的，例如：
 *   S 用「仨」而不是网上常见的「扨」（后者会把「日」判成 s）；T 用「他」（「它」会漏掉「他」）；
 *   M 用「呣」（「痳」会把「林」系判成 m）；J 用「丌」、W 用「屲」、D 用「咑」、O 用「喔」、B 用「丷」。
 *
 * 已知局限：
 *   - 多音字取排序规则里的那个读音：「行」→ x、「长」→ z、「重」→ z，所以「银行」是 yx、「长度」是 zd；
 *   - 排序数据里没有读音的生僻字会排到最后，被归到 z；
 *   - 浏览器不支持中文排序（resolvedOptions().locale 不是 zh 开头）时整体退化：汉字原样返回，只剩原文匹配。
 *
 * ─────────────────────────── 自检用例（输入 → 期望） ───────────────────────────
 * pinyinInitials：
 *   1. '定时任务'          → 'dsrw'
 *   2. '仪表板'            → 'ybb'
 *   3. '主题工坊'          → 'ztgf'
 *   4. '执行日志'          → 'zxrz'      （「日」是边界难点）
 *   5. '每日数据备份'      → 'mrsjbf'
 *   6. '他们'              → 'tm'        （「他」是边界难点）
 *   7. '林'                → 'l'         （「林」系与 m 的边界）
 *   8. '环境变量'          → 'hjbl'
 *   9. '订阅管理'          → 'dygl'
 *  10. '检查 SSL 证书'     → 'jc ssl zs'  （非汉字原样转小写，空格保留，长度与原文逐字对应）
 *  11. 'Open API'          → 'open api'
 *  12. '吖阿击挖哦搭巴拿妈' → 'aajwodbnm' （各边界字本身）
 * matchPinyin（只看是否命中与命中位置）：
 *  13. ('定时任务', 'dsrw')        → hits [0,1,2,3]
 *  14. ('定时任务', '任务')        → hits [2,3]，score 比 13 高（原文命中优先）
 *  15. ('检查 SSL 证书', 'jcsslzs') → 命中（跳过空格），hits [0,1,3,4,5,7,8]
 *  16. ('运行 · 每日数据备份', '备份') → hits [9,10]
 *  17. ('脚本管理', 'xyz')         → null
 *  18. ('定时任务', 'd任')         → 命中（拼音与汉字混输，按子序列），hits [0,2]
 *
 * 运行方法：开发模式（npm run dev）打开面板，在浏览器控制台粘贴：
 *   const m = await import('/src/utils/pinyinInitials.ts')
 *   const ini = [['定时任务','dsrw'],['仪表板','ybb'],['主题工坊','ztgf'],['执行日志','zxrz'],['每日数据备份','mrsjbf'],
 *     ['他们','tm'],['林','l'],['环境变量','hjbl'],['订阅管理','dygl'],['检查 SSL 证书','jc ssl zs'],['Open API','open api'],
 *     ['吖阿击挖哦搭巴拿妈','aajwodbnm']]
 *   console.table(ini.map(([t, want]) => ({ t, want, got: m.pinyinInitials(t), ok: m.pinyinInitials(t) === want })))
 *   const mt = [['定时任务','dsrw','0,1,2,3'],['定时任务','任务','2,3'],['检查 SSL 证书','jcsslzs','0,1,3,4,5,7,8'],
 *     ['运行 · 每日数据备份','备份','9,10'],['脚本管理','xyz',''],['定时任务','d任','0,2']]
 *   console.table(mt.map(([t, q, want]) => { const r = m.matchPinyin(t, q); const got = r ? r.hits.join(',') : ''; return { t, q, want, got, score: r?.score, ok: got === want } }))
 *   // 额外核对 14 比 13 分高：
 *   m.matchPinyin('定时任务', '任务').score > m.matchPinyin('定时任务', 'dsrw').score
 * ──────────────────────────────────────────────────────────────────────────────
 */

const LETTERS = 'abcdefghjklmnopqrstwxyz'
// 与 LETTERS 逐个对应的边界字（每个字母在拼音排序里的第一个汉字），顺序不能乱
const BOUNDARY_CHARS = Array.from('吖丷嚓咑妸发旮哈丌咔垃呣拏喔妑七呥仨他屲夕丫帀')

const HAN_RE = /\p{Script=Han}/u

let collator: Intl.Collator | null | undefined
// 单字结果缓存：命令面板每敲一个键都要把几十个标题重算一遍，缓存后几乎零开销
const cache = new Map<string, string>()

/** 单个字符的首字母：汉字给拼音首字母，其他字符原样转小写；无法判断的汉字原样返回 */
function initialOf(ch: string): string {
  const hit = cache.get(ch)
  if (hit !== undefined) return hit

  let result = ch.toLowerCase()
  // toLowerCase 个别字符会变成两个码元（如 İ），那样会打乱逐字对应，直接保留原字符
  if (Array.from(result).length !== 1) result = ch

  if (HAN_RE.test(ch)) {
    if (collator === undefined) {
      // 懒创建；运行环境不认中文排序时记成 null，之后汉字都原样返回
      try {
        const c = new Intl.Collator('zh-Hans-CN', { sensitivity: 'base' })
        collator = c.resolvedOptions().locale.toLowerCase().startsWith('zh') ? c : null
      } catch {
        collator = null
      }
    }
    if (collator && collator.compare(ch, BOUNDARY_CHARS[0]!) >= 0) {
      // 二分找「最后一个 <= ch 的边界字」
      let lo = 0
      let hi = BOUNDARY_CHARS.length - 1
      while (lo < hi) {
        const mid = (lo + hi + 1) >> 1
        if (collator.compare(ch, BOUNDARY_CHARS[mid]!) >= 0) lo = mid
        else hi = mid - 1
      }
      result = LETTERS[lo]!
    } else {
      result = ch
    }
  }

  cache.set(ch, result)
  return result
}

/**
 * 整段文字的首字母串，与原文【逐字对应】（按 Array.from 的字符算，长度相同），
 * 所以在首字母串里命中的位置可以直接拿来高亮原文。
 * '检查 SSL 证书' → 'jc ssl zs'
 */
export function pinyinInitials(text: string): string {
  return Array.from(text).map(initialOf).join('')
}

/**
 * 用输入词匹配一段文字，命中返回 { score, hits }（hits 是原文里要高亮的字符下标，按 Array.from 计），否则 null。
 * 空白不参与匹配（输入和原文里的空格都忽略），所以「jcsslzs」能命中「检查 SSL 证书」。
 * 三档，分数从高到低：
 *   1. 原文连续命中（不区分大小写）：100，从开头命中再 +20；
 *   2. 首字母串连续命中：70，从开头命中再 +20；
 *   3. 子序列命中（每个输入字符等于原文字符或它的首字母即可，可以拼音汉字混输）：30。
 * 同档里文字越短分越高一点点（-0.1/字），让「定时任务」排在「定时任务日志」前面。
 */
export function matchPinyin(text: string, query: string): { score: number; hits: number[] } | null {
  const q = Array.from(query.toLowerCase().replace(/\s+/g, ''))
  if (q.length === 0) return null

  // 去掉空白后的逐字表：raw 是小写原字符，ini 是首字母，idx 是它在原文里的下标
  const raw: string[] = []
  const ini: string[] = []
  const idx: number[] = []
  Array.from(text).forEach((ch, i) => {
    if (/\s/.test(ch)) return
    const lower = ch.toLowerCase()
    raw.push(Array.from(lower).length === 1 ? lower : ch)
    ini.push(initialOf(ch))
    idx.push(i)
  })
  const lengthPenalty = raw.length * 0.1

  // 第 1、2 档：在逐字表上找连续命中（逐字比较，不用 indexOf，避免四字节字符把下标算歪）
  for (const [table, base] of [[raw, 100], [ini, 70]] as const) {
    for (let start = 0; start + q.length <= table.length; start++) {
      let ok = true
      for (let k = 0; k < q.length; k++) {
        if (table[start + k] !== q[k]) {
          ok = false
          break
        }
      }
      if (ok) {
        return {
          score: base + (start === 0 ? 20 : 0) - lengthPenalty,
          hits: idx.slice(start, start + q.length),
        }
      }
    }
  }

  // 第 3 档：子序列，贪心从左往右
  const hits: number[] = []
  let k = 0
  for (let j = 0; j < raw.length && k < q.length; j++) {
    if (raw[j] === q[k] || ini[j] === q[k]) {
      hits.push(idx[j]!)
      k++
    }
  }
  if (k < q.length) return null
  return { score: 30 - lengthPenalty, hits }
}
