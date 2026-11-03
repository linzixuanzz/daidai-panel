// 通行密钥（Passkey / WebAuthn）的浏览器侧工具，登录页与设置页共用（#175）。
//
// 后端给的 options 就是 go-webauthn 的 publicKey 那一层，二进制字段都是 base64url（无填充）；
// navigator.credentials.create / get 要的是 ArrayBuffer，拿回来的凭据也全是 ArrayBuffer，
// 要再编码成 base64url 交给后端。
// 刻意不用 PublicKeyCredential.parseCreationOptionsFromJSON / toJSON：老浏览器（Safari 17 以前、Firefox 119 以前）没有。

// 主机名是不是 IP 字面量：IPv4（1.2.3.4），或带方括号的 IPv6（location.hostname 给的是 [::1] 这种形式）
function isIPLiteralHost(hostname: string): boolean {
  if (hostname.startsWith('[')) return true
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname)
}

/**
 * 当前页面能不能用通行密钥，口径与后端判断访问地址的规则完全一致：
 * - https 访问，且主机名是域名（不是 IP）；
 * - 或 http 访问，且主机名恰好是 localhost。
 * 注意：浏览器其实也把 http://127.0.0.1、http://[::1]、http://foo.localhost 当成安全上下文，
 * 前端只看 isSecureContext 会放行，但后端不收（RP ID 不能是 IP，http 只放行 localhost），点了只会回 400，
 * 所以这里按后端口径直接判不支持，让按钮置灰并说明原因。
 */
export function isPasskeySupported(): boolean {
  if (typeof window === 'undefined') return false
  if (window.isSecureContext !== true || !window.PublicKeyCredential || !navigator.credentials) return false
  const protocol = window.location.protocol
  const hostname = window.location.hostname.toLowerCase()
  if (protocol === 'https:') return !!hostname && !isIPLiteralHost(hostname)
  if (protocol === 'http:') return hostname === 'localhost'
  return false
}

/** base64url（可带可不带填充）→ ArrayBuffer */
export function base64urlToBuffer(value: string): ArrayBuffer {
  const base64 = String(value || '').replace(/-/g, '+').replace(/_/g, '/')
  // atob 要求长度是 4 的倍数，后端给的是无填充的，这里补齐
  const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4)
  const binary = atob(padded)
  return Uint8Array.from(binary, (ch) => ch.charCodeAt(0)).buffer as ArrayBuffer
}

/** ArrayBuffer / TypedArray → base64url（无填充） */
export function bufferToBase64url(buffer: ArrayBuffer | ArrayBufferView): string {
  const bytes = buffer instanceof ArrayBuffer
    ? new Uint8Array(buffer)
    : new Uint8Array(buffer.buffer, buffer.byteOffset, buffer.byteLength)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

// 后端约定直接给 publicKey 那一层；万一外面还包着 { publicKey: ... }（库的 CredentialCreation 整体），这里也认
function unwrapPublicKey(options: any) {
  return options && typeof options === 'object' && options.publicKey ? options.publicKey : options
}

/** 注册：把后端的 options 转成 navigator.credentials.create({ publicKey }) 要的结构 */
export function toCreationOptions(options: any): PublicKeyCredentialCreationOptions {
  const raw = unwrapPublicKey(options) || {}
  const publicKey: any = { ...raw }
  publicKey.challenge = base64urlToBuffer(raw.challenge)
  publicKey.user = { ...raw.user, id: base64urlToBuffer(raw.user?.id) }
  // excludeCredentials：该用户在本域名下已有的通行密钥，浏览器据此拦住「同一台设备重复添加」
  if (Array.isArray(raw.excludeCredentials)) {
    publicKey.excludeCredentials = raw.excludeCredentials.map((item: any) => ({
      ...item,
      id: base64urlToBuffer(item.id),
    }))
  }
  return publicKey as PublicKeyCredentialCreationOptions
}

/** 登录：把后端的 options 转成 navigator.credentials.get({ publicKey }) 要的结构 */
export function toRequestOptions(options: any): PublicKeyCredentialRequestOptions {
  const raw = unwrapPublicKey(options) || {}
  const publicKey: any = { ...raw }
  publicKey.challenge = base64urlToBuffer(raw.challenge)
  // 后端用的是可发现凭据，allowCredentials 一般为空；有值时同样要解码
  if (Array.isArray(raw.allowCredentials)) {
    publicKey.allowCredentials = raw.allowCredentials.map((item: any) => ({
      ...item,
      id: base64urlToBuffer(item.id),
    }))
  }
  return publicKey as PublicKeyCredentialRequestOptions
}

// 两种凭据共有的外层字段
function credentialBaseJSON(credential: PublicKeyCredential) {
  return {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    // 老浏览器没有这两个成员，取不到就不带（JSON.stringify 会丢掉 undefined）
    authenticatorAttachment: credential.authenticatorAttachment || undefined,
    clientExtensionResults:
      typeof credential.getClientExtensionResults === 'function' ? credential.getClientExtensionResults() : {},
  }
}

/** 注册：navigator.credentials.create 的结果 → register/finish 要的 credential JSON */
export function registrationCredentialToJSON(credential: PublicKeyCredential) {
  const response = credential.response as AuthenticatorAttestationResponse
  // getTransports 是后加的方法，老浏览器没有；有就带上，登录时浏览器能据此选对传输方式
  const transports = typeof response.getTransports === 'function' ? response.getTransports() : undefined
  return {
    ...credentialBaseJSON(credential),
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      attestationObject: bufferToBase64url(response.attestationObject),
      ...(transports && transports.length > 0 ? { transports } : {}),
    },
  }
}

/** 登录：navigator.credentials.get 的结果 → login/finish 要的 credential JSON */
export function assertionCredentialToJSON(credential: PublicKeyCredential) {
  const response = credential.response as AuthenticatorAssertionResponse
  return {
    ...credentialBaseJSON(credential),
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      authenticatorData: bufferToBase64url(response.authenticatorData),
      signature: bufferToBase64url(response.signature),
      // 可发现凭据一定带 userHandle；万一没有就不带，由后端判失败
      ...(response.userHandle ? { userHandle: bufferToBase64url(response.userHandle) } : {}),
    },
  }
}

/**
 * 用户在系统弹窗里点了取消、或者等超时了。
 * 浏览器两种情况都抛 NotAllowedError（主动中止是 AbortError），分不开，统一只提示「已取消」，不当错误弹红。
 */
export function isPasskeyCancelError(err: any): boolean {
  return err?.name === 'NotAllowedError' || err?.name === 'AbortError'
}

/** 通行密钥流程里的异常 → 一句中文提示（先看后端的 error，再看浏览器抛的 DOMException） */
export function resolvePasskeyErrorMessage(err: any, fallback: string): string {
  const serverError = err?.response?.data?.error
  if (typeof serverError === 'string' && serverError.trim()) return serverError.trim()
  // 注册时 excludeCredentials 命中：这台设备 / 这个密码管理器里已经有这个账号的通行密钥了
  if (err?.name === 'InvalidStateError') return '这台设备上已经添加过这个账号的通行密钥了'
  // RP ID 与当前域名对不上（例如反代改了域名），或页面不在安全上下文里
  if (err?.name === 'SecurityError') return '当前访问地址不支持通行密钥，请通过 HTTPS 域名（或 http://localhost）访问面板'
  if (err?.name === 'NotSupportedError') return '当前浏览器或设备不支持通行密钥'
  return fallback
}
