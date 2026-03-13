import request from './request'

export const systemApi = {
  info: () => request.get('/system/info'),
  dashboard: () => request.get('/system/dashboard'),
  stats: () => request.get('/system/stats'),
  version: () => request.get('/system/version'),
  publicVersion: () => request.get('/system/public-version'),
  checkUpdate: () => request.get('/system/check-update'),
  updatePanel: () => request.post('/system/update'),
  panelLog: (params?: { lines?: number; keyword?: string }) =>
    request.get('/system/panel-log', { params }),
  backup: (password?: string) => request.post('/system/backup', { password }),
  backupList: () => request.get('/system/backups'),
  downloadBackup: (filename: string) => `/api/system/backup/download/${filename}`,
  restore: (filename: string, password?: string) =>
    request.post('/system/restore', { filename, password }),
  deleteBackup: (filename: string) =>
    request.delete('/system/backup', { params: { filename } }),
}

export const configApi = {
  list: () => request.get('/configs'),
  get: (key: string) => request.get(`/configs/${key}`),
  set: (data: { key: string; value: string; description?: string }) => request.post('/configs', data),
  batchSet: (configs: Record<string, string>) => request.put('/configs/batch', { configs }),
  delete: (key: string) => request.delete(`/configs/${key}`),
}

export const platformTokenApi = {
  platforms: () => request.get('/platform-tokens/platforms'),
  createPlatform: (data: { name: string; label?: string; icon?: string }) =>
    request.post('/platform-tokens/platforms', data),
  deletePlatform: (id: number) => request.delete(`/platform-tokens/platforms/${id}`),
  list: (platformId?: number) =>
    request.get('/platform-tokens', { params: platformId ? { platform_id: platformId } : {} }),
  create: (data: { platform_id: number; name: string; token: string; remarks?: string }) =>
    request.post('/platform-tokens', data),
  update: (id: number, data: { name?: string; token?: string; remarks?: string }) =>
    request.put(`/platform-tokens/${id}`, data),
  delete: (id: number) => request.delete(`/platform-tokens/${id}`),
  enable: (id: number) => request.put(`/platform-tokens/${id}/enable`),
  disable: (id: number) => request.put(`/platform-tokens/${id}/disable`),
}
