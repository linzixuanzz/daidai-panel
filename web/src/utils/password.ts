// 设置新密码的复杂度规则，与后端 server/pkg/validator 的 ValidateNewPassword 保持一致：
// 长度 8~128（按字符数），字母、数字、其他符号三类里至少含两类。
// 只用在「设置新密码」的地方（初始化、改密、建用户、重置密码），登录不校验，老用户的弱密码照样能登。

// 文案与后端 NewPasswordRuleMessage 逐字相同，前后端提示一致。
export const NEW_PASSWORD_RULE_MESSAGE = '密码需 8-128 位，且字母、数字、符号至少包含两种'

// 输入框占位提示用的短句
export const NEW_PASSWORD_PLACEHOLDER = '8-128 位，字母/数字/符号至少两种'

export function isValidNewPassword(password: string): boolean {
  // 按 Unicode 字符数算长度（Array.from 按码点拆分，emoji 这类代理对只算一个），与后端 utf8.RuneCountInString 一致
  const length = Array.from(password).length
  if (length < 8 || length > 128) return false

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
