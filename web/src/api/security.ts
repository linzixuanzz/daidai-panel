import request from './request'

// 通行密钥列表里的一行（#175）。后端只下发这几个字段，公钥 / 用户句柄 / credential JSON 一律不出接口
export interface PasskeyItem {
  id: number
  name: string
  rp_id: string
  // 添加时的完整访问地址（含端口），如 https://panel.example.com、http://localhost:5792；只能在这个地址下用它登录
  origin: string
  created_at: string
  last_used_at: string | null
  backup_state: boolean
}

export const userApi = {
  list() {
    return request.get('/users') as Promise<{ data: any[] }>
  },

  create(data: { username: string; password: string; role?: string }) {
    return request.post('/users', data) as Promise<{ message: string; data: any }>
  },

  update(id: number, data: { role?: string; enabled?: boolean }) {
    return request.put(`/users/${id}`, data) as Promise<{ message: string; data: any }>
  },

  delete(id: number) {
    return request.delete(`/users/${id}`) as Promise<{ message: string }>
  },

  resetPassword(id: number, password: string) {
    return request.put(`/users/${id}/reset-password`, { password }) as Promise<{ message: string }>
  }
}

export const securityApi = {
  loginLogs(params?: { page?: number; page_size?: number; username?: string }) {
    return request.get('/security/login-logs', { params }) as Promise<{ data: any[]; total: number; page: number; page_size: number }>
  },

  clearLoginLogs() {
    return request.delete('/security/login-logs') as Promise<{ message: string }>
  },

  sessions() {
    return request.get('/security/sessions') as Promise<{ data: any[] }>
  },

  revokeSession(id: number) {
    return request.delete(`/security/sessions/${id}`) as Promise<{ message: string }>
  },

  revokeAllSessions() {
    return request.delete('/security/sessions/others') as Promise<{ message: string }>
  },

  ipWhitelist() {
    return request.get('/security/ip-whitelist') as Promise<{ data: any[] }>
  },

  addIPWhitelist(data: { ip: string; remarks?: string }) {
    return request.post('/security/ip-whitelist', data) as Promise<{ message: string; data: any }>
  },

  removeIPWhitelist(id: number) {
    return request.delete(`/security/ip-whitelist/${id}`) as Promise<{ message: string }>
  },

  auditLogs(params?: { page?: number; page_size?: number; action?: string }) {
    return request.get('/security/audit-logs', { params }) as Promise<{ data: any[]; total: number; page: number; page_size: number }>
  },

  loginStats(days?: number) {
    return request.get('/security/login-stats', { params: { days } }) as Promise<{ data: any }>
  },

  setup2FA() {
    return request.post('/security/2fa/setup') as Promise<{ data: { secret: string; uri: string } }>
  },

  verify2FA(code: string) {
    return request.post('/security/2fa/verify', { code }) as Promise<{ message: string }>
  },

  disable2FA(code: string) {
    return request.delete('/security/2fa', { data: { code } }) as Promise<{ message: string }>
  },

  get2FAStatus() {
    return request.get('/security/2fa/status') as Promise<{ data: { enabled: boolean } }>
  },

  // ---- 通行密钥（#175）：任何角色都只能管自己的 ----

  listPasskeys() {
    return request.get('/security/passkeys') as Promise<{ data: PasskeyItem[] }>
  },

  // 添加第一步：必须带当前密码（防会话被劫持后偷偷加一个通行密钥长期驻留），密码错回 400「密码错误」。
  // 开了两步验证时还要带 totp_code：缺码回 400 code=two_factor_required，码错回 400 code=invalid_totp
  passkeyRegisterBegin(password: string, totpCode?: string) {
    // 没开两步验证时不带 totp_code 这个键
    const body: Record<string, string> = { password }
    if (totpCode) body.totp_code = totpCode
    return request.post('/security/passkeys/register/begin', body) as Promise<{
      data: { session_id: string; options: any }
    }>
  },

  // 添加第二步：交回 navigator.credentials.create 的结果；name 留空时后端用「通行密钥」
  passkeyRegisterFinish(sessionId: string, name: string, credential: Record<string, unknown>) {
    return request.post('/security/passkeys/register/finish', {
      session_id: sessionId,
      name,
      credential
    }) as Promise<{ message: string; data: PasskeyItem }>
  },

  renamePasskey(id: number, name: string) {
    return request.put(`/security/passkeys/${id}`, { name }) as Promise<{ message: string }>
  },

  deletePasskey(id: number) {
    return request.delete(`/security/passkeys/${id}`) as Promise<{ message: string }>
  }
}
