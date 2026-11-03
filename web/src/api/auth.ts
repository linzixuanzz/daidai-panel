import request from './request'
import axios from 'axios'
import type { GeeTestValidateResult } from '@/utils/geetest'

export const authApi = {
  checkInit() {
    return request.get('/auth/check-init') as Promise<{ need_init: boolean }>
  },

  init(username: string, password: string) {
    return request.post('/auth/init', { username, password }) as Promise<{ message: string; user: any }>
  },

  login(username: string, password: string, totpCode?: string, captcha?: GeeTestValidateResult | null) {
    return request.post('/auth/login', {
      username,
      password,
      totp_code: totpCode || '',
      captcha: captcha || undefined
    }) as Promise<{
      message: string
      access_token: string
      refresh_token: string
      user: any
      two_factor_required?: boolean
      captcha_required?: boolean
    }>
  },

  // 通行密钥登录第一步（#175，免登录）：拿一次性的 session_id 与 publicKey 请求选项（二进制字段是 base64url）。
  // 访问地址不是 HTTPS 域名 / localhost 时回 400 code=passkey_unsupported_origin。
  passkeyLoginBegin() {
    return request.post('/auth/passkey/login/begin', {}) as Promise<{
      data: { session_id: string; options: any }
    }>
  },

  // 通行密钥登录第二步：把 navigator.credentials.get 的结果交回去。
  // 成功时响应体与 /auth/login 成功时逐字段相同；会话过期 400 passkey_session_expired，校验失败 401 invalid_passkey
  passkeyLoginFinish(sessionId: string, credential: Record<string, unknown>) {
    return request.post('/auth/passkey/login/finish', {
      session_id: sessionId,
      credential
    }) as Promise<{
      message: string
      access_token: string
      refresh_token: string
      user: any
    }>
  },

  logout() {
    return request.post('/auth/logout') as Promise<{ message: string }>
  },

  refresh() {
    const refreshToken = localStorage.getItem('refresh_token')
    return axios.post('/api/auth/refresh', null, {
      headers: {
        Authorization: `Bearer ${refreshToken}`,
        'X-Client-Type': 'web',
        'X-Client-App': 'daidai-panel-web'
      }
    }).then(res => res.data) as Promise<{ access_token: string }>
  },

  getUser() {
    return request.get('/auth/user') as Promise<{ user: any }>
  },

  changePassword(oldPassword: string, newPassword: string) {
    return request.put('/auth/password', {
      old_password: oldPassword,
      new_password: newPassword
    }) as Promise<{ message: string }>
  },

  captchaConfig(username?: string) {
    return request.get('/auth/captcha-config', {
      params: username ? { username } : undefined
    }) as Promise<{
      enabled: boolean
      captcha_id: string
      configured: boolean
      implemented: boolean
      required: boolean
      require_after_failures: number
      message: string
    }>
  },

  uploadAvatar(file: File) {
    const formData = new FormData()
    formData.append('avatar', file)
    return request.post('/auth/avatar', formData, {
      headers: { 'Content-Type': 'multipart/form-data' }
    }) as Promise<{ message: string; avatar_url: string }>
  },

  deleteAvatar() {
    return request.delete('/auth/avatar') as Promise<{ message: string }>
  },

  changeUsername(username: string) {
    return request.put('/auth/username', { username }) as Promise<{ message: string; user: any }>
  },

  // 当前登录用户的界面偏好：editor 一组（编辑器）+ list 一组（列表页每页条数与视图栏显隐，v3.3.1）。
  // 只要登录就能读写自己的那份，不限角色 —— 脚本页 operator 就能进，
  // 不能要求管理员权限才允许改自己的编辑器开关。
  // stored 表示「服务端是否真的存过这个用户的【editor】偏好」，与 list 组无关。它不是冗余字段：
  // 服务端对「没存过」的用户下发的是一整套默认值，与「用户主动选了默认值」逐字相同，
  // 客户端只有靠这个标记才能区分两者 —— 没存过时要把本机偏好上行迁移，
  // 而不是拿默认值把本机（可能是 v3.2.2 存下来的）选择覆盖掉。
  // list 是稀疏对象：只含用户存过且合法的键；老服务端（v3.3.0 及以前）没有这个字段，
  // utils/listPreferences.ts 靠「list 是不是对象」判断服务端认不认识这一组。
  getPreferences() {
    return request.get('/auth/preferences') as Promise<{
      editor: Record<string, any>
      stored?: boolean
      list?: Record<string, unknown>
    }>
  },

  // 只提交改动过的键，服务端做字段级合并，两个标签页各改各的开关不会互相覆盖。
  updatePreferences(editor: Record<string, string | boolean>) {
    return request.put('/auth/preferences', { editor }) as Promise<{
      editor: Record<string, any>
      stored?: boolean
      list?: Record<string, unknown>
    }>
  },

  // 列表页偏好（utils/listPreferences.ts 专用）。body 只带 list 一组：服务端按组可选写入，
  // editor 列一个字节都不碰，editor 的 stored 也不会因此翻成 true。
  // 同样只提交改动过的键，服务端逐键合并；值的类型必须对（条数 number、环境变量条数 string、开关 bool），
  // 白名单外的值服务端回 400。
  // 🔴 只能发给认识 list 的服务端（v3.3.1+）：老服务端会无视 list、把整套 editor 默认值写进库。
  updateListPreferences(list: Record<string, string | number | boolean>) {
    return request.put('/auth/preferences', { list }) as Promise<{
      editor: Record<string, any>
      stored?: boolean
      list?: Record<string, unknown>
    }>
  }
}
