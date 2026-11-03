// 设置新密码的复杂度规则，与后端 server/pkg/validator 的 ValidateNewPassword 保持一致：
// 至少 8 个字符、且 UTF-8 编码不超过 72 字节（bcrypt 的硬上限），字母、数字、其他符号三类里至少含两类。
// 只用在「设置新密码」的地方（初始化、改密、建用户、重置密码），登录不校验，老用户的弱密码照样能登。

// 文案与后端 NewPasswordRuleMessage 逐字相同，前后端提示一致。
export const NEW_PASSWORD_RULE_MESSAGE = '密码需 8-72 位，且字母、数字、符号至少包含两种'

// 输入框占位提示用的短句
export const NEW_PASSWORD_PLACEHOLDER = '8-72 位，字母/数字/符号至少两种'

// bcrypt 只接受不超过 72 字节的密码，按 UTF-8 字节数算（中文一个字占 3 字节）
const NEW_PASSWORD_MAX_BYTES = 72

export function isValidNewPassword(password: string): boolean {
  // 下限按 Unicode 字符数算（Array.from 按码点拆分，emoji 这类代理对只算一个），与后端 utf8.RuneCountInString 一致
  if (Array.from(password).length < 8) return false
  // 上限按 UTF-8 字节数算，与后端 len(password) 一致
  if (new TextEncoder().encode(password).length > NEW_PASSWORD_MAX_BYTES) return false

  let hasLetter = false
  let hasDigit = false
  let hasOther = false
  for (const ch of password) {
    if (/\p{L}/u.test(ch)) hasLetter = true
    else if (/\p{Nd}/u.test(ch)) hasDigit = true
    else hasOther = true
  }
  return [hasLetter, hasDigit, hasOther].filter(Boolean).length >= 2
}
