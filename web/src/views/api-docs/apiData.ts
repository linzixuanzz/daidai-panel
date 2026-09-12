export interface ApiParam {
  name: string
  type: string
  required?: boolean
  description: string
  example?: string
}

export interface ApiEndpoint {
  id: string
  method: 'GET' | 'POST' | 'PUT' | 'DELETE'
  path: string
  title: string
  description: string
  auth?: 'jwt' | 'open_api' | 'none' | 'ticket'
  pathParams?: ApiParam[]
  queryParams?: ApiParam[]
  bodyParams?: ApiParam[]
  helperExamples?: Record<string, string>
  responseExample?: string
  responseFields?: ApiParam[]
  responseContentType?: string
}

export interface ApiCategory {
  key: string
  label: string
  endpoints: ApiEndpoint[]
}

export const apiCategories: ApiCategory[] = [
  {
    key: 'plugin-guide',
    label: '插件对接指南',
    endpoints: [
      {
        id: 'plugin-overview',
        method: 'GET',
        path: '/docs/plugin-overview',
        title: '对接概述',
        description: '呆呆面板通过 Open API 应用机制对接第三方插件，实现环境变量的自动管理。首先在面板「Open API」页面创建应用，获取 App Key 和 App Secret。配置格式：Host丨AppKey丨AppSecret（使用中文竖线 丨 分隔），示例：http://127.0.0.1:5173丨a1b2c3d4...丨e5f6g7h8...。对接流程：1. 使用 App Key + App Secret 获取 Token → 2. 使用 Token 操作环境变量（增删改查）→ 3. Token 过期（24小时）后重新获取。',
        auth: 'none',
        responseExample: JSON.stringify({
          '配置格式': 'Host丨AppKey丨AppSecret',
          '配置示例': 'http://127.0.0.1:5173丨a1b2c3d4e5f6...丨e5f6g7h8i9j0...',
          '认证方式': 'Open API Token (Bearer)',
          '接口前缀': '/api/',
          'Token有效期': '24 小时，过期后重新调用 token 接口获取',
          '对接流程': [
            '1. 在面板「Open API」页面创建应用，获取 App Key 和 App Secret',
            '2. POST /api/open-api/token 获取 access_token',
            '3. GET /api/envs 查询环境变量',
            '4. POST /api/envs 添加环境变量',
            '5. PUT /api/envs/:id 更新环境变量',
            '6. DELETE /api/envs/:id 删除环境变量',
          ],
        }, null, 2),
        responseFields: [
          { name: 'Host', type: 'string', description: '呆呆面板地址，如 http://127.0.0.1:5173' },
          { name: 'AppKey', type: 'string', description: '应用的 App Key（在面板 Open API 页面创建应用后获取）' },
          { name: 'AppSecret', type: 'string', description: '应用的 App Secret（创建应用时生成）' },
          { name: 'Token', type: 'string', description: '通过 App Key + Secret 获取的 JWT Token，有效期 24 小时' },
        ],
      },
      {
        id: 'plugin-login',
        method: 'POST',
        path: '/api/open-api/token',
        title: '获取 Open API Token',
        description: '插件对接的第一步：使用在面板「Open API」页面创建的应用 App Key 和 App Secret 获取 access_token。Token 有效期 24 小时，过期后需重新调用此接口获取新 Token（无 refresh_token 机制）。',
        auth: 'none',
        bodyParams: [
          { name: 'app_key', type: 'string', required: true, description: '应用 App Key', example: 'a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4' },
          { name: 'app_secret', type: 'string', required: true, description: '应用 App Secret', example: 'e5f6g7h8i9j0e5f6g7h8i9j0e5f6g7h8i9j0e5f6g7h8i9j0e5f6g7h8i9j0k1l2' },
        ],
        responseExample: JSON.stringify({
          data: {
            access_token: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...',
            token_type: 'Bearer',
            expires_in: 86400,
          },
        }, null, 2),
        responseFields: [
          { name: 'data.access_token', type: 'string', description: 'JWT 访问令牌，有效期 24 小时，需携带在请求头 Authorization: Bearer {token}' },
          { name: 'data.token_type', type: 'string', description: '令牌类型，固定为 Bearer' },
          { name: 'data.expires_in', type: 'integer', description: '令牌有效期（秒），固定为 86400（24小时）' },
        ],
      },
      {
        id: 'plugin-find-env',
        method: 'GET',
        path: '/api/envs',
        title: '查找环境变量',
        description: '通过关键字搜索环境变量，用于判断变量是否已存在。插件通常通过 keyword 参数按变量名搜索，然后遍历结果匹配 remarks 中的用户标识来定位具体变量。也可以直接用 names 参数按变量名精确匹配，省掉「模糊结果再过滤一遍」这一步。完整的筛选参数见「环境变量 → 获取所有环境变量」。',
        auth: 'jwt',
        queryParams: [
          { name: 'keyword', type: 'string', description: '搜索关键字（按变量名 / 变量值 / 备注 / 分组模糊匹配，四者是 OR）', example: 'sfsyUrl' },
          { name: 'names', type: 'string', description: '按变量名【精确】筛选，多个用逗号分隔或重复传 names=A&names=B。比 keyword 更适合插件定位：keyword 是模糊匹配，会把 sfsyUrl2 一起带出来，names 是精确相等。与 keyword 之间是 AND', example: 'sfsyUrl' },
          { name: 'page', type: 'integer', description: '页码，默认 1', example: '1' },
          { name: 'page_size', type: 'integer', description: '每页数量，建议设为 100 以获取完整列表（上限就是 100，超过会回落成 20；要一次拿全请改用 all=1）', example: '100' },
        ],
        responseExample: JSON.stringify({
          data: [{
            id: 1,
            name: 'sfsyUrl',
            value: 'sessionId=xxx&_login_mobile_=xxx',
            remarks: '顺丰:账号1丨用户:123丨手机:138****1234丨到期:2026-12-31',
            created_at: '2026-03-13T10:00:00',
            updated_at: '2026-03-13T10:00:00',
          }],
          total: 1,
          page: 1,
          page_size: 100,
        }, null, 2),
        responseFields: [
          { name: 'data', type: 'array', description: '环境变量列表' },
          { name: 'data[].id', type: 'integer', description: '变量 ID，更新和删除时需要' },
          { name: 'data[].name', type: 'string', description: '变量名' },
          { name: 'data[].value', type: 'string', description: '变量值' },
          { name: 'data[].remarks', type: 'string', description: '备注，可存储用户标识等信息' },
          { name: 'total', type: 'integer', description: '总数' },
        ],
      },
      {
        id: 'plugin-add-env',
        method: 'POST',
        path: '/api/envs',
        title: '添加环境变量',
        description: '当查找不到已有变量时，调用此接口添加新的环境变量。注意：呆呆面板不需要对 value 进行 URL 编码，直接传递原始值即可。',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '变量名', example: 'sfsyUrl' },
          { name: 'value', type: 'string', required: true, description: '变量值（无需 URL 编码）', example: 'sessionId=xxx&_login_mobile_=xxx' },
          { name: 'remarks', type: 'string', description: '备注，建议存储用户标识信息', example: '顺丰:账号1丨用户:123丨手机:138****1234丨到期:2026-12-31' },
          { name: 'group', type: 'string', description: '分组名称', example: '顺丰' },
        ],
        responseExample: JSON.stringify({
          message: '创建成功',
          data: { id: 1, name: 'sfsyUrl', value: 'sessionId=xxx&_login_mobile_=xxx' },
        }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '操作结果消息' },
          { name: 'data', type: 'object', description: '创建的环境变量信息' },
          { name: 'data.id', type: 'integer', description: '新创建的变量 ID' },
        ],
      },
      {
        id: 'plugin-update-env',
        method: 'PUT',
        path: '/api/envs/:id',
        title: '更新环境变量',
        description: '当查找到已有变量时，调用此接口更新变量值。注意：ID 通过 URL 路径传递，不是放在请求体中。',
        auth: 'jwt',
        pathParams: [
          { name: 'id', type: 'integer', required: true, description: '环境变量 ID（通过查找接口获取）' },
        ],
        bodyParams: [
          { name: 'name', type: 'string', description: '变量名', example: 'sfsyUrl' },
          { name: 'value', type: 'string', description: '新的变量值', example: 'sessionId=new_value&_login_mobile_=new_mobile' },
          { name: 'remarks', type: 'string', description: '备注', example: '顺丰:账号1丨用户:123丨手机:138****1234丨到期:2026-12-31' },
          { name: 'group', type: 'string', description: '分组名称', example: '顺丰' },
        ],
        responseExample: JSON.stringify({ message: '更新成功' }, null, 2),
      },
      {
        id: 'plugin-delete-env',
        method: 'DELETE',
        path: '/api/envs/:id',
        title: '删除环境变量',
        description: '删除指定的环境变量。注意：呆呆面板通过路径参数传递 ID，不是请求体数组。',
        auth: 'jwt',
        pathParams: [
          { name: 'id', type: 'integer', required: true, description: '环境变量 ID' },
        ],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
      {
        id: 'plugin-python-example',
        method: 'POST',
        path: '/api/open-api/token',
        title: 'Python 完整示例',
        description: '以下是完整的 Python 类封装 DaiDaiPanel，可直接在插件中使用。通过 Open API 的 App Key 和 App Secret 认证，支持查找/添加/更新/删除环境变量，以及智能的添加或更新逻辑。使用方法：panel = DaiDaiPanel(host, app_key, app_secret)，然后调用 panel.add_or_update_env(name, value, remarks, keyword) 即可自动判断添加或更新。',
        auth: 'none',
        bodyParams: [
          { name: 'host', type: 'string', required: true, description: '面板地址', example: 'http://127.0.0.1:5173' },
          { name: 'app_key', type: 'string', required: true, description: '应用 App Key', example: 'a1b2c3d4e5f6...' },
          { name: 'app_secret', type: 'string', required: true, description: '应用 App Secret', example: 'e5f6g7h8i9j0...' },
        ],
        responseExample: `# DaiDaiPanel 完整示例代码
import requests
from typing import Optional, Dict

class DaiDaiPanel:
    def __init__(self, host: str, app_key: str, app_secret: str):
        self.host = host.rstrip('/')
        self.app_key = app_key
        self.app_secret = app_secret
        self.access_token = None
        self._get_token()

    def _get_token(self):
        url = f'{self.host}/api/open-api/token'
        data = {"app_key": self.app_key, "app_secret": self.app_secret}
        res = requests.post(url, json=data, timeout=10)
        res.raise_for_status()
        result = res.json()
        self.access_token = result['data']['access_token']

    def _headers(self) -> Dict[str, str]:
        return {
            "Authorization": f"Bearer {self.access_token}",
            "Content-Type": "application/json"
        }

    def _request(self, method, url, **kwargs):
        res = getattr(requests, method)(url, headers=self._headers(), timeout=10, **kwargs)
        if res.status_code == 401:
            self._get_token()
            res = getattr(requests, method)(url, headers=self._headers(), timeout=10, **kwargs)
        return res

    def find_env(self, name: str, keyword: str = "") -> Optional[int]:
        url = f"{self.host}/api/envs?keyword={name}&page_size=100"
        res = self._request("get", url)
        for env in res.json().get('data', []):
            if env['name'] == name:
                if not keyword or keyword in env.get('remarks', ''):
                    return env['id']
        return None

    def add_env(self, name: str, value: str, remarks: str = "") -> bool:
        url = f"{self.host}/api/envs"
        data = {"name": name, "value": value, "remarks": remarks}
        res = self._request("post", url, json=data)
        return res.status_code == 200

    def update_env(self, env_id: int, name: str, value: str, remarks: str = "") -> bool:
        url = f"{self.host}/api/envs/{env_id}"
        data = {"name": name, "value": value, "remarks": remarks}
        res = self._request("put", url, json=data)
        return res.status_code == 200

    def delete_env(self, env_id: int) -> bool:
        url = f"{self.host}/api/envs/{env_id}"
        res = self._request("delete", url)
        return res.status_code == 200

    def add_or_update_env(self, name, value, remarks="", keyword=""):
        env_id = self.find_env(name, keyword)
        if env_id:
            return self.update_env(env_id, name, value, remarks)
        return self.add_env(name, value, remarks)

# 使用示例
panel = DaiDaiPanel(
    "http://127.0.0.1:5173",
    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
    "e5f6g7h8i9j0e5f6g7h8i9j0e5f6g7h8i9j0e5f6g7h8i9j0e5f6g7h8i9j0k1l2"
)
panel.add_or_update_env(
    name="sfsyUrl",
    value="sessionId=xxx&_login_mobile_=xxx",
    remarks="顺丰:账号1丨手机:138****1234丨到期:2026-12-31",
    keyword="账号1"
)`,
        responseFields: [
          { name: 'DaiDaiPanel(host, key, secret)', type: 'class', description: '初始化并通过 Open API 获取 Token' },
          { name: '_get_token()', type: 'method', description: '获取/刷新 Open API Token' },
          { name: '_request(method, url, ...)', type: 'method', description: '自动处理 401 重新获取 Token 的请求封装' },
          { name: 'find_env(name, keyword)', type: 'method', description: '查找环境变量，返回 ID 或 None' },
          { name: 'add_env(name, value, remarks)', type: 'method', description: '添加环境变量' },
          { name: 'update_env(id, name, value, remarks)', type: 'method', description: '更新环境变量' },
          { name: 'delete_env(id)', type: 'method', description: '删除环境变量' },
          { name: 'add_or_update_env(name, value, ...)', type: 'method', description: '智能添加或更新（推荐使用）' },
        ],
      },
      {
        id: 'plugin-notes',
        method: 'GET',
        path: '/docs/plugin-notes',
        title: '注意事项与常见问题',
        description: 'Token 管理：access_token 有效期 24 小时，过期后重新调用 POST /api/open-api/token 获取新 Token（无 refresh_token 机制）。在 Open API 的鉴权接口上收到 401 即表示 Token 过期，需重新获取。⚠️ 这条约定不适用于 POST /api/auth/login：登录接口的 401 还可能承载两步验证 / 人机验证挑战（响应体带 two_factor_required 或 captcha_required 及 code 字段），不能一律当成 Token 过期去续期，更不能在 401 时直接抛异常丢掉响应体。错误格式：{"error": "错误信息"} + HTTP 状态码（400/401/403/404/500），登录相关失败额外带机器可读的 code 字段。最佳实践：1. 不需要对 value 进行 URL 编码 2. 建议设置 10 秒超时 3. 建议对「已鉴权接口」实现 401 自动重新获取 Token，但登录接口要先读响应体再判断 4. 在 remarks 中存储用户标识便于查找 5. 查询时设置较大的 page_size（如 100）减少请求次数。',
        auth: 'none',
        responseExample: JSON.stringify({
          '差异对比': {
            '认证方式': '青龙: Client ID + Secret → 呆呆: App Key + App Secret',
            'Token获取': '青龙: GET /open/auth/token → 呆呆: POST /api/open-api/token',
            'Token有效期': '青龙: 不固定 → 呆呆: 24 小时',
            '添加变量': '青龙: POST /open/envs (数组) → 呆呆: POST /api/envs (对象)',
            '更新变量': '青龙: PUT /open/envs (body带id) → 呆呆: PUT /api/envs/{id} (路径参数)',
            '删除变量': '青龙: DELETE /open/envs (数组) → 呆呆: DELETE /api/envs/{id} (路径参数)',
            'Value编码': '青龙: 需要 URL 编码 → 呆呆: 不需要编码',
          },
          '错误响应格式': { error: '错误信息' },
          'HTTP状态码': {
            '200': '成功',
            '400': '参数错误',
            '401': '已鉴权接口：Token 过期或无效；/api/auth/login：凭据错误，或需要两步验证 / 人机验证（先读响应体的 code 字段）',
            '403': '应用已禁用',
            '404': '不存在',
            '429': '请求过于频繁，或账号已被锁定（code = account_locked）',
            '500': '服务器错误',
          },
        }, null, 2),
        responseFields: [
          { name: 'Token 过期', type: 'tip', description: '已鉴权接口收到 401 时，重新调用 POST /api/open-api/token 获取新 Token' },
          { name: '登录接口的 401', type: 'tip', description: 'POST /api/auth/login 的 401 不是 Token 过期。它可能是凭据错误，也可能是两步验证 / 人机验证挑战（two_factor_required / captcha_required）。请按响应体的 code 字段分支，不要在 401 时直接抛异常丢掉响应体' },
          { name: '应用禁用', type: 'tip', description: '收到 403 表示应用已被禁用，需在面板 Open API 页面重新启用' },
          { name: 'Value 编码', type: 'tip', description: '不需要 URL 编码，直接传原始值' },
          { name: '批量操作', type: 'tip', description: '暂不支持批量，需循环调用单个接口' },
          { name: '超时设置', type: 'tip', description: '建议所有请求设置 10 秒超时' },
          { name: '用户标识', type: 'tip', description: '在 remarks 中存储用户标识，查询时通过 keyword 过滤' },
        ],
      },
    ],
  },
  {
    key: 'auth',
    label: '认证',
    endpoints: [
      {
        id: 'auth-login',
        method: 'POST',
        path: '/api/auth/login',
        title: '用户登录',
        description: '使用用户名和密码登录，获取 JWT Token。⚠️ 本接口的 401 不等于「Token 过期」：账号开启两步验证或人机验证时，登录挑战同样用 401 承载，响应体里带 two_factor_required / captcha_required 与稳定的 code 字段。客户端必须先读响应体再决定下一步，不能把 401 一律当成需要续期而把响应体丢掉——若 HTTP 客户端（如 dio 的 validateStatus、axios 的 validateStatus）在 401 时直接抛异常，用户会看到「请输入两步验证码」却没有任何可输入的地方。收到 two_factor_required 时，请带上 totp_code 重发本接口；若同时带回 captcha_required=true，第二次请求还需重新完成人机验证。',
        auth: 'none',
        bodyParams: [
          { name: 'username', type: 'string', required: true, description: '用户名', example: 'admin' },
          { name: 'password', type: 'string', required: true, description: '密码', example: 'admin123' },
          { name: 'totp_code', type: 'string', required: false, description: '两步验证动态码（6 位）。仅在上一次请求返回 two_factor_required 时需要携带', example: '123456' },
          { name: 'captcha', type: 'object', required: false, description: '人机验证载荷（极验 4），字段：lot_number / captcha_output / pass_token / gen_time。仅在返回 captcha_required 时需要携带' },
        ],
        responseExample: JSON.stringify({
          '登录成功 (200)': {
            access_token: 'eyJhbGciOi...',
            refresh_token: 'eyJhbGciOi...',
            user: { id: 1, username: 'admin', role: 'admin' },
          },
          '需要两步验证码 (401)': {
            error: '请输入两步验证码',
            code: 'two_factor_required',
            two_factor_required: true,
            captcha_required: false,
            captcha_id: '',
            captcha_threshold: 0,
            require_after_failures: 0,
          },
          '两步验证码错误 (401)': {
            error: '两步验证码错误',
            code: 'invalid_totp',
            two_factor_required: true,
          },
          '用户名或密码错误 (401)': {
            error: '用户名或密码错误',
            code: 'invalid_credentials',
            failed_attempts: 1,
          },
          '账号已锁定 (429)': {
            error: '账号已锁定，请 15 分钟后重试',
            code: 'account_locked',
            locked: true,
            remaining_seconds: 900,
          },
        }, null, 2),
        responseFields: [
          { name: 'access_token', type: 'string', description: 'JWT 访问令牌（仅登录成功时返回）' },
          { name: 'refresh_token', type: 'string', description: 'JWT 刷新令牌（仅登录成功时返回）' },
          { name: 'user', type: 'object', description: '用户信息（仅登录成功时返回）' },
          { name: 'code', type: 'string', description: '登录失败的稳定机器可读标识，取值：two_factor_required / invalid_totp / invalid_credentials / captcha_required / account_locked。建议客户端按 code 分支，不要匹配中文文案' },
          { name: 'error', type: 'string', description: '失败原因的中文提示，可直接展示给用户' },
          { name: 'two_factor_required', type: 'boolean', description: '为 true 表示需要两步验证码，请带 totp_code 重发本接口' },
          { name: 'captcha_required', type: 'boolean', description: '为 true 表示本次登录需要完成人机验证；两步验证挑战同样会回带该字段' },
          { name: 'captcha_id', type: 'string', description: '极验 4 的 Captcha ID，前端初始化验证码组件时使用' },
          { name: 'require_after_failures', type: 'number', description: '触发人机验证所需的失败次数，0 表示每次登录都需要' },
          { name: 'failed_attempts', type: 'number', description: '当前 IP + 用户名累计的连续失败次数' },
        ],
      },
      {
        id: 'auth-refresh',
        method: 'POST',
        path: '/api/auth/refresh',
        title: '刷新令牌',
        description: '使用 refresh_token 获取新的 access_token',
        auth: 'jwt',
        responseExample: JSON.stringify({ access_token: 'eyJhbGciOi...' }, null, 2),
      },
      {
        id: 'auth-me',
        method: 'GET',
        path: '/api/auth/user',
        title: '获取当前用户信息',
        description: '获取当前登录用户的详细信息',
        auth: 'jwt',
        responseExample: JSON.stringify({
          user: { id: 1, username: 'admin', role: 'admin', enabled: true, created_at: '2026-01-01T00:00:00' },
        }, null, 2),
      },
      {
        id: 'auth-password',
        method: 'PUT',
        path: '/api/auth/password',
        title: '修改密码',
        description: '修改当前用户密码',
        auth: 'jwt',
        bodyParams: [
          { name: 'old_password', type: 'string', required: true, description: '当前密码' },
          { name: 'new_password', type: 'string', required: true, description: '新密码（至少8位）' },
        ],
        responseExample: JSON.stringify({ message: '密码修改成功' }, null, 2),
      },
      {
        id: 'auth-preferences-get',
        method: 'GET',
        path: '/api/auth/preferences',
        title: '获取当前用户的界面偏好',
        description: '读取当前登录用户自己的界面偏好，目前只有编辑器（脚本编辑 / 日志查看）那一组开关。⚠️ 这是【按用户】的偏好，不是全面板配置：每个账号各存一份、互不影响，也不在 /api/configs 里。鉴权只要求 JWTAuth、不限角色——operator / viewer 同样能读写自己那份，改自己的编辑器开关不该要管理员权限（对比 /api/configs 那组是管理员独占）。从没存过偏好的用户直接返回下面这份默认值，不会 404，但会带上 stored=false（见响应字段，这是升级路径的关键）。⚠️ 编辑器引擎（CodeMirror / Monaco）刻意【不在】这里：它带着设备自适应语义（触摸设备与窄屏硬回落 CodeMirror），只存在浏览器本地——引擎跟设备走，偏好跟人走。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          editor: {
            word_wrap: 'on',
            minimap: false,
            indent_guides: true,
            whitespace: 'selection',
            indent_width: 'auto',
          },
          stored: false,
        }, null, 2),
        responseFields: [
          { name: 'editor.word_wrap', type: 'string', description: '自动换行，取值 on / off，默认 on' },
          { name: 'editor.minimap', type: 'boolean', description: '缩略图开关，JSON 布尔，默认 false。只对 Monaco 引擎生效（CodeMirror 没有缩略图）' },
          { name: 'editor.indent_guides', type: 'boolean', description: '缩进参考线开关，JSON 布尔，默认 true' },
          { name: 'editor.whitespace', type: 'string', description: '空白符显示，取值 none（从不）/ selection（仅选中）/ all（始终），默认 selection' },
          { name: 'editor.indent_width', type: 'string', description: '缩进宽度，取值 auto / 2 / 4 / 6 / 8，默认 auto（按文件内容自动检测）。⚠️ 传输层是字符串，是 "4" 不是 4' },
          { name: 'stored', type: 'boolean', description: '服务端是否真的存过这个用户的偏好。判定口径：有这个用户的记录 且 记录里的偏好能解析成 JSON 对象 → true；没有记录 / 空串 / 脏 JSON 一律 false（后两种当成「没存过」）。⚠️ 它是给客户端做升级路径判断用的：stored=false 说明服务端还没有这个用户的记录，此时客户端应当把【本机】那份偏好一次性 PUT 上去做上行迁移，而不是拿下面 editor 里这套默认值去覆盖本机——照着覆盖的话，老用户升级到有服务端偏好的版本时，本机调好的开关会在首屏被静默冲掉。stored=true 才允许拿 editor 的值写回本地。注意 editor 字段本身不受 stored 影响：无论 stored 是什么都下发一整套可用的值，不认得 stored 的老客户端行为与加这个字段之前逐字一致' },
        ],
      },
      {
        id: 'auth-preferences-update',
        method: 'PUT',
        path: '/api/auth/preferences',
        title: '更新当前用户的界面偏好',
        description: '更新当前登录用户自己的界面偏好。⚠️ 字段级合并：body 里只放要改的键，没传的键保持原值、不会被重置成默认值——面板自己就是这么用的（改一个开关只提交那一个键），两个标签页各改各的开关不会互相覆盖。鉴权同 GET：JWTAuth、不限角色，改的永远是调用者自己那份，没有「替别人改」的入口。任一字段取值非法返回 400，不做静默纠正。响应体是合并之后的完整偏好，与 GET 逐字同形。',
        auth: 'jwt',
        bodyParams: [
          { name: 'editor', type: 'object', required: true, description: '编辑器偏好，取下面 5 个键的任意子集', example: '{ "whitespace": "all" }' },
          { name: 'editor.word_wrap', type: 'string', description: '自动换行，取值 on / off', example: 'on' },
          { name: 'editor.minimap', type: 'boolean', description: '缩略图开关（只对 Monaco 引擎生效）。写入时是 JSON 布尔——面板前端提交的就是布尔；另外也接受 "on" / "off" / "true" / "false" 字符串，那是留给 APP 与历史客户端的。读出来统一是 JSON 布尔', example: 'false' },
          { name: 'editor.indent_guides', type: 'boolean', description: '缩进参考线开关。写入时是 JSON 布尔（面板前端提交的就是布尔），同样兼容 "on" / "off" / "true" / "false" 字符串以照顾 APP 与历史客户端。读出来统一是 JSON 布尔', example: 'true' },
          { name: 'editor.whitespace', type: 'string', description: '空白符显示，字符串，取值 none / selection / all', example: 'all' },
          { name: 'editor.indent_width', type: 'string', description: '缩进宽度，字符串，取值 auto / 2 / 4 / 6 / 8。⚠️ 传输层是字符串，要传 "4" 不是 4', example: 'auto' },
        ],
        responseExample: JSON.stringify({
          editor: {
            word_wrap: 'on',
            minimap: false,
            indent_guides: true,
            whitespace: 'all',
            indent_width: 'auto',
          },
          stored: true,
        }, null, 2),
        responseFields: [
          { name: 'editor', type: 'object', description: '合并之后的完整偏好，字段说明见「获取当前用户的界面偏好」' },
          { name: 'stored', type: 'boolean', description: '恒为 true——写完这一次，服务端必然已经有这个用户的记录了。含义与 GET 的 stored 相同（有记录且能解析 → true），客户端据此判断「服务端还没有这个用户的记录」并决定要不要做本机偏好上行迁移；PUT 之后这个判断的答案只可能是「已经有了」' },
        ],
      },
      {
        id: 'auth-2fa-status',
        method: 'GET',
        path: '/api/security/2fa/status',
        title: '查询两步验证状态',
        description: '查询当前登录用户是否已启用两步验证（TOTP）',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: { enabled: true } }, null, 2),
        responseFields: [
          { name: 'data.enabled', type: 'boolean', description: '是否已启用两步验证' },
        ],
      },
      {
        id: 'auth-2fa-setup',
        method: 'POST',
        path: '/api/security/2fa/setup',
        title: '生成两步验证密钥',
        description: '为当前登录用户生成新的 TOTP 密钥并返回 otpauth 链接。此时两步验证仍处于「未启用」状态，需要再调用 /api/security/2fa/verify 输入动态码确认后才真正生效。重复调用会覆盖上一次尚未确认的密钥。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          data: {
            secret: 'JBSWY3DPEHPK3PXP',
            uri: 'otpauth://totp/DaiDaiPanel:admin?secret=JBSWY3DPEHPK3PXP&issuer=DaiDaiPanel&digits=6&period=30',
          },
        }, null, 2),
        responseFields: [
          { name: 'data.secret', type: 'string', description: 'Base32 编码的 TOTP 密钥，可手动录入验证器 App' },
          { name: 'data.uri', type: 'string', description: 'otpauth 链接，用于生成二维码；6 位动态码、30 秒周期' },
        ],
      },
      {
        id: 'auth-2fa-verify',
        method: 'POST',
        path: '/api/security/2fa/verify',
        title: '确认并启用两步验证',
        description: '输入验证器 App 当前显示的动态码，确认密钥已正确录入后启用两步验证。启用后再次登录会先收到 401 + two_factor_required 挑战。',
        auth: 'jwt',
        bodyParams: [
          { name: 'code', type: 'string', required: true, description: '验证器当前显示的 6 位动态码', example: '123456' },
        ],
        responseExample: JSON.stringify({ message: '2FA 已启用' }, null, 2),
      },
      {
        id: 'auth-2fa-disable',
        method: 'DELETE',
        path: '/api/security/2fa',
        title: '关闭两步验证',
        description: '关闭当前登录用户的两步验证。必须提供当前动态码——仅凭一个被劫持的会话不足以摘掉两步验证。若验证器已丢失，可在服务器上执行 ddp disable-2fa <用户名> 兜底。',
        auth: 'jwt',
        bodyParams: [
          { name: 'code', type: 'string', required: true, description: '验证器当前显示的 6 位动态码', example: '123456' },
        ],
        responseExample: JSON.stringify({ message: '2FA 已禁用' }, null, 2),
      },
    ],
  },
  {
    key: 'tasks',
    label: '定时任务',
    endpoints: [
      {
        id: 'tasks-list',
        method: 'GET',
        path: '/api/tasks',
        title: '获取任务列表',
        description: '获取所有定时任务，支持关键字搜索和分页。每行可能多带一个 schedule_hint（string）：它只在「这条任务其实不会自动触发」时才出现，正常任务上这个 key 完全不存在。取值只有两种：「该任务类型不会自动触发，只能手动运行」（手动运行/开机运行类型）、「未注册到调度器，重新保存一次任务即可恢复」（cron 任务但调度器里没有它的触发条目）。已禁用的任务一律不带；面板没起调度器时不下发第二种。默认排序（不传 sort_rules 时）依次是：置顶 → 状态分组（启用/排队中/运行中一组、禁用一组、其余一组）→ 运行中优先 → 拖拽顺序（list_order）→ 手工顺序（sort_order）→ 创建时间倒序 → ID 倒序，其中「运行中优先」的边界见下方 sort_rules 的说明',
        auth: 'jwt',
        queryParams: [
          { name: 'keyword', type: 'string', description: '搜索关键字' },
          { name: 'page', type: 'integer', description: '页码，默认 1', example: '1' },
          { name: 'page_size', type: 'integer', description: '每页数量，默认 20', example: '20' },
          { name: 'label', type: 'string', description: '按标签模糊匹配（labels LIKE %值%）。分组要连前缀一起传，例如「分组:日常」，只传「日常」会把普通标签也捞进来' },
          { name: 'filters', type: 'string', description: '任务视图的筛选规则，JSON 字符串数组 [{field,operator,value}]。field 可选 command / name / cron_expression / status / labels / subscription；operator 可选 contains / not_contains / equals / not_equals；匹配一律先 trim 再转小写、不是正则；多条之间是 AND。status 的 value 用 1（已启用）/ 0（已禁用）/ 2（运行中）/ 0.5（排队中）。value 为空的规则会被整条丢弃，JSON 解析失败则静默降级为不筛选', example: '[{"field":"command","operator":"contains","value":"jd"}]' },
          { name: 'sort_rules', type: 'string', description: '任务视图的排序规则，JSON 字符串数组 [{field,direction}]。field 可选 name / command / cron_expression / status / labels / subscription / created_at / last_run_at / next_run_at；direction 只认 desc，其它一律按 asc。⚠️ 置顶与状态分组【压在排序规则之上】，排序规则只在同一分区内生效：先按置顶分区（置顶的整体在前），再按状态分区（启用/排队中/运行中一组、禁用一组、其余一组），所以「按最后运行倒序」不会把置顶任务冲散、也不会把禁用任务混进启用任务中间。⚠️ 状态分区有一条【豁免】：当 sort_rules 的【第一条】规则 field = "status" 时，状态分区不生效，整份列表按 status 值原样排序（置顶分区仍然生效，不受影响）。理由是状态分区本身就是按 status 分的，再按 status 排一次等于自相矛盾——用户显式点了「按状态排序」，拿到的却是被状态分区钉死的固定组序。只看第一条是因为它才代表用户的主排序意图，第二条起是 tie-break。分区内多条规则按先后做 tie-break，全平手再回落默认排序：运行中优先 > 拖拽顺序（list_order）> 手工顺序（sort_order）> 创建时间。⚠️「运行中优先」与上面的置顶 / 状态分区【不是同一层】，别混为一谈：置顶与状态分区压在【所有】排序规则之上，而运行中优先只是默认排序链里的一环 —— 不传 sort_rules 时它把 status=2（运行中）的任务提到【本区】最前，传了 sort_rules 时只有规则全部打平才轮得到它。所以用户显式点了「名称 A→Z」，运行中不会越过他的规则插到前面。这一层只认 2（运行中），不认 0.5（排队中）——排队是几秒钟就过去的瞬时态，一并提上来只会让同一次运行多跳一次；任务跑完自动回到原位，list_order 不会被改写（拖拽顺序不受影响，PUT /api/tasks/sort 的「区」划分也不受影响）。last_run_at（最后运行）与 next_run_at（下次运行）是任务列表页两列的点击排序：next_run_at 不是数据库列，是按 cron 现算的快照，只能在内存里排；这两个字段的空值（从未运行、已禁用或非 cron 因而没有下次运行）在各自分区内一律排最后，不随 asc/desc 翻转。未知 field 静默回落默认排序，不报 400', example: '[{"field":"next_run_at","direction":"asc"}]' },
        ],
        responseExample: JSON.stringify({
          data: [{
            id: 1,
            name: '签到任务',
            command: 'task sign.py',
            cron_expression: '0 9 * * *',
            status: 1,
            last_run_status: 0,
            last_run_at: '2026-03-10T09:00:00',
            notify_on_abort: false,
            notification_channel_id: 3,
            notification_channel_name: 'Telegram 主通道',
          }],
          total: 1, page: 1, page_size: 20,
        }, null, 2),
      },
      {
        id: 'tasks-create',
        method: 'POST',
        path: '/api/tasks',
        title: '创建任务',
        description: '创建新的定时任务',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '任务名称', example: '签到任务' },
          { name: 'command', type: 'string', required: true, description: '执行命令', example: 'task sign.py' },
          { name: 'task_type', type: 'string', description: '任务类型：cron / manual / startup', example: 'cron' },
          { name: 'cron_expression', type: 'string', description: 'Cron 表达式；task_type=cron 时必填', example: '0 9 * * *' },
          { name: 'timeout', type: 'integer', description: '超时时间（秒），0 表示永不超时', example: '0' },
          { name: 'max_retries', type: 'integer', description: '最大重试次数', example: '0' },
          { name: 'retry_interval', type: 'integer', description: '重试间隔（秒）', example: '5' },
          { name: 'notify_on_failure', type: 'boolean', description: '失败时通知', example: 'true' },
          { name: 'notify_on_success', type: 'boolean', description: '成功时通知', example: 'false' },
          { name: 'notify_on_abort', type: 'boolean', description: '主动终止时通知', example: 'false' },
          { name: 'notification_channel_id', type: 'integer', description: '指定通知渠道 ID，留空则发送到全部「默认推送」渠道（push_scope=default）', example: '3' },
        ],
        responseExample: JSON.stringify({ message: '创建成功', data: { id: 1, name: '签到任务' } }, null, 2),
      },
      {
        id: 'tasks-update',
        method: 'PUT',
        path: '/api/tasks/:id',
        title: '更新任务',
        description: '更新指定任务的配置',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        bodyParams: [
          { name: 'name', type: 'string', description: '任务名称' },
          { name: 'command', type: 'string', description: '执行命令' },
          { name: 'task_type', type: 'string', description: '任务类型：cron / manual / startup' },
          { name: 'cron_expression', type: 'string', description: 'Cron 表达式' },
          { name: 'timeout', type: 'integer', description: '超时时间（秒），0 表示永不超时' },
          { name: 'max_retries', type: 'integer', description: '最大重试次数' },
          { name: 'retry_interval', type: 'integer', description: '重试间隔（秒）' },
          { name: 'notify_on_failure', type: 'boolean', description: '失败时通知' },
          { name: 'notify_on_success', type: 'boolean', description: '成功时通知' },
          { name: 'notify_on_abort', type: 'boolean', description: '主动终止时通知' },
          { name: 'notification_channel_id', type: 'integer', description: '指定通知渠道 ID，设为 null 则恢复为发送到全部「默认推送」渠道' },
        ],
        responseExample: JSON.stringify({ message: '更新成功' }, null, 2),
      },
      {
        id: 'tasks-delete',
        method: 'DELETE',
        path: '/api/tasks/:id',
        title: '删除任务',
        description: '删除指定任务，需要 operator 及以上角色。任务的执行日志会一起删除，删除后无法恢复；正在运行的这次执行不会被中断。默认只删任务、不动脚本文件。带上 delete_script=1 时，服务端会从这条任务的 command 按真实执行口径解析出脚本，逐项判断能不能一起删：被其他任务（含已禁用的）共用、位于订阅目录、是脚本目录根下的 notify.py / sendNotify.js 或全局钩子 task_before.sh / task_after.sh / extra.sh、路径经过软链接、位于 .git / node_modules 等受保护目录、任务正在运行或排队中……任何不确定的情况都保留，只删能确定没人再用的普通文件（不删目录、不动版本历史）。请求里不接受「要删哪个文件」，confirm_script_path 只能收窄范围、不能扩大。建议先调 POST /api/tasks/delete-preview 看清会删什么、会保留什么。用 Open API 应用令牌带 delete_script 时，应用的 scopes 除 tasks 外还必须包含 scripts（或 *），否则返回 403「应用令牌缺少 scripts 权限，不能同时删除脚本」，任务和脚本都不会动。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        queryParams: [
          { name: 'delete_script', type: 'boolean', description: '是否同时删除任务的脚本文件。按 Go 的 strconv.ParseBool 解析：1 / t / true（含 T / TRUE / True）为开；0、false、非法值（如 abc）或空值一律按「没开」处理，走原逻辑、不报 400', example: '1' },
          { name: 'confirm_script_path', type: 'string', description: '确认要删除的脚本路径，只在 delete_script 为真时有意义，只能收窄范围。不传：删除服务端判定为可删的脚本（给不先调预览的调用方用）；传了：只有与服务端解析出的 path 完全相等（大小写敏感）时才删，否则保留并标 not_confirmed；传空值（confirm_script_path=）：一个都不删。请原样回传预览接口给的 path，不要自己拼', example: 'ops/clean.sh' },
        ],
        responseExample: JSON.stringify({
          '不带 delete_script（与旧版逐字节相同）': { message: '删除成功' },
          '带 delete_script=1': {
            message: '删除成功',
            scripts: {
              tasks: [{ id: 13, name: '清理临时文件', found: true, running: false, script_status: 'resolved', script_path: 'ops/clean.sh', note: '' }],
              deleted: [{ path: 'ops/clean.sh', task_ids: [13], deletable: true, reason: '', detail: '', shared_by: [], warnings: [] }],
              skipped: [],
            },
          },
        }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '恒为「删除成功」，带不带 delete_script 都一样。任务不存在时返回 404「任务不存在」（带不带开关都一样，此时什么都不删）' },
          { name: 'scripts', type: 'object', description: '只有 delete_script 为真时才出现。tasks：本次任务的解析结果（字段同 POST /api/tasks/delete-preview 的 tasks[]）；deleted：已删除的脚本；skipped：保留下来的脚本，reason 是机器可读的原因、detail 是给人看的中文说明（可原样展示）。deleted / skipped 的每一项字段同预览接口的 scripts[]' },
          { name: '不带参数时', type: 'tip', description: '不带 delete_script（或它为假）时，响应与旧版逐字节一致：只有 {"message":"删除成功"}，没有 scripts 字段，脚本文件原样保留。APP 与老的 Open API 调用方无需任何改动' },
          { name: 'reason 取值', type: 'tip', description: '判定原因（预览与执行都会出现，按此顺序取第一个命中的，例外见下）：check_failed（检查出错，为安全起见保留）→ hidden_path（位于 .git、node_modules 等受保护目录）→ symlink（路径经过软链接）→ not_regular_file（不是普通文件，例如名字像脚本的文件夹）→ managed_helper（脚本目录根下的 notify.py / sendNotify.js；脚本目录根下的 task_before.sh / task_after.sh / extra.sh 全局钩子也一律保留）→ subscription_managed（订阅管理的文件）→ task_not_deleted（仅执行阶段：任务行没删掉）→ shared（仍被其他任务使用）→ referenced_in_hook（被其他任务的前置/后置命令或订阅的拉取前/后命令提到）→ task_running（任务正在运行或排队中）。执行阶段另有：not_confirmed（不在确认列表里）/ changed（确认之后文件变了）/ not_found（文件已经不存在）/ remove_failed（删除失败，详情见面板日志）。顺序有两处例外：一是执行阶段先把文件与确认时的状态比对，文件没了报 not_found、被换过报 changed，之后才做上面这串结构性复核（脚本目录解析失败、读取任务或订阅失败这类环境错误除外，它们始终最先判、直接 check_failed）；二是预览时读取文件状态（Lstat）出错、逐段检查路径上的软链接 / 目录联接时出错，也按 check_failed 处理，但排在 hidden_path 之后才判定。deletable 当且仅当 reason 为空时为 true；以后可能新增取值，遇到不认识的请按「保留」处理并展示 detail' },
          { name: '路径口径', type: 'tip', description: 'path / script_path 一律是相对脚本目录的正斜杠路径（如 jd/sign.js），不回显绝对路径，也不回显底层错误原文；所有数组恒为 []，不会是 null' },
          { name: '批量删除的开关', type: 'tip', description: '批量入口 PUT /api/tasks/batch（action=delete）与 DELETE /api/tasks/batch/delete 的开关叫 delete_scripts，放在 JSON 请求体里，必须是 JSON bool（true / false）。传成字符串 "true" 会让整个请求绑定失败、返回 400「请求参数错误」，任务也不会被删' },
        ],
      },
      {
        id: 'tasks-delete-preview',
        method: 'POST',
        path: '/api/tasks/delete-preview',
        title: '预览删除任务时的脚本处理',
        description: '只读接口，不删任何东西：给一批任务 ID，返回每个任务解析出的脚本、这些脚本能否随任务一起删除、不能删的原因。需要 operator 及以上角色（viewer 返回 403「权限不足」）。脚本路径只从任务的 command 按真实执行口径解析，判定规则与 DELETE /api/tasks/:id 带 delete_script 时完全一致。面板的删除弹窗先调它展示「将删除 / 将保留」，用户勾选后再把 deletable=true 的 path 原样作为 confirm_script_path(s) 传给删除接口。用 Open API 应用令牌调用时，scopes 必须同时包含 scripts（或 *），否则返回 403「应用令牌缺少 scripts 权限，不能同时删除脚本」。',
        auth: 'jwt',
        bodyParams: [
          { name: 'task_ids', type: 'array', required: true, description: '任务 ID 数组（非负整数）。按首次出现的顺序去重，去重后为空返回 400「请选择要删除的任务」；缺字段或不是整数数组返回 400「请求参数错误」。不存在的 ID 不报错，对应项的 script_status 为 task_not_found', example: '[12,15,99]' },
        ],
        responseExample: JSON.stringify({
          data: {
            checked: true,
            tasks: [
              { id: 12, name: '京东签到', found: true, running: false, script_status: 'resolved', script_path: 'jd/sign.js', note: '' },
              { id: 15, name: '模块任务', found: true, running: false, script_status: 'no_script', script_path: '', note: '命令运行的是 Python 模块 foo，没有对应的脚本文件。' },
              { id: 99, name: '', found: false, running: false, script_status: 'task_not_found', script_path: '', note: '任务不存在（可能已被删除）。' },
            ],
            scripts: [
              { path: 'jd/sign.js', task_ids: [12], deletable: false, reason: 'shared', detail: '仍被 1 个其他任务使用：京东签到（备用），删除后它会无法运行。', shared_by: [{ id: 31, name: '京东签到（备用）', text_match: false }], warnings: ['文件名 sign 常见于公共库（如 sendNotify、utils、sign），可能被其他脚本引用，确认不再需要再删。'] },
            ],
          },
        }, null, 2),
        responseFields: [
          { name: 'data.checked', type: 'boolean', description: '恒为 true。是哨兵字段：调用方靠它区分「真的查过」和「没查成」（例如面板是旧版、没有这个接口）' },
          { name: 'data.tasks', type: 'array', description: '按请求顺序，每个 ID 一项：id / name / found / running / script_status / script_path / note。script_status=task_not_found 表示任务不存在；found=false 也可能是查库出错、无法确认；running=true 表示任务正在运行或排队中（删除任务不会中断这次执行，它用到的脚本会保留）；note 是给人看的说明' },
          { name: 'data.tasks[].script_status', type: 'string', description: 'resolved（解析出脚本，一定出现在 scripts 里）/ no_script（命令是依赖命令或 python -m 模块，没有脚本文件）/ not_found（命令里的脚本已经不存在）/ outside_scripts_dir（脚本不在脚本目录内，面板不会删）/ unresolved（命令格式有误，识别不出脚本；查库出错、无法确认任务时也是它，此时 found=false）/ task_not_found（任务不存在）' },
          { name: 'data.tasks[].script_path', type: 'string', description: '只在 resolved 与 not_found 时有值（not_found 时只用于展示）；其余状态为空串' },
          { name: 'data.scripts', type: 'array', description: '按真实文件合并（多个选中的任务用同一个脚本只出现一项，task_ids 全部列出），按 path 升序。每项：path / task_ids / deletable / reason / detail / shared_by / warnings；reason 的全部取值见 DELETE /api/tasks/:id' },
          { name: 'data.scripts[].shared_by', type: 'array', description: '本次范围外、仍在引用这个文件的任务 [{id, name, text_match}]，含已禁用的。只要有共用方就会填，不论 reason 是什么。text_match=true 表示那个任务的命令不是直接运行这个脚本（或者命令当前跑不起来），但命令文本里指向了它' },
          { name: 'data.scripts[].warnings', type: 'array', description: '只提示、不阻断删除，例如文件名常见于公共库（sendNotify、utils、sign 等），可能被其他脚本 require / import' },
          { name: '预览只是参考', type: 'tip', description: '真正删除时，服务端会基于删除后剩下的任务重新判定，再按 confirm 列表收窄。预览之后有人改了命令、复制了任务，结果以删除时为准（多出来的共用方会让脚本被保留，不会多删）' },
        ],
      },
      {
        id: 'tasks-batch',
        method: 'PUT',
        path: '/api/tasks/batch',
        title: '批量操作任务',
        description: '对多个任务执行同一个动作，需要 operator 及以上角色（Web 任务页的批量操作走这里）。逐个处理：不存在的 ID 静默跳过，个别任务处理不了（例如启用时校验不通过）也只是跳过，不会让整批报错；run 遇到已经在运行中的任务不会重复触发，但仍计入 count。action=delete 时会连同执行日志一起删除，正在运行的这次执行不会被中断；再带上 delete_scripts=true 可同时删除这些任务的脚本，判定规则与 DELETE /api/tasks/:id 的 delete_script 完全一致（建议先调 POST /api/tasks/delete-preview）。用 Open API 应用令牌带 delete_scripts 时，scopes 还必须包含 scripts（或 *），否则返回 403「应用令牌缺少 scripts 权限，不能同时删除脚本」，任务和脚本都不会动。',
        auth: 'jwt',
        bodyParams: [
          { name: 'ids', type: 'array', required: true, description: '任务 ID 数组', example: '[12,13]' },
          { name: 'action', type: 'string', required: true, description: 'enable（启用）/ disable（禁用）/ delete（删除，连同执行日志）/ run（立即运行）/ stop（停止运行中的任务）/ pin（置顶）/ unpin（取消置顶）。取值区分大小写，也不去首尾空格；不在上面列表里的 action（如 Delete、RUN）不报错、什么都不做，但仍按查到的任务计入 count，message 里原样带出它（如「批量Delete: 1 个任务」）', example: 'delete' },
          { name: 'delete_scripts', type: 'boolean', description: '是否同时删除脚本，只在 action=delete 时生效，其它 action 带了也会被忽略（响应里不会出现 scripts）。必须是 JSON bool：传成字符串 "true" 会让整个请求绑定失败，返回 400「请求参数错误」，任务也不会被删', example: 'true' },
          { name: 'confirm_script_paths', type: 'array', description: '确认要删除的脚本路径列表，只能收窄范围。不传或传 null：删除服务端判定为可删的全部脚本；传了：只删与列表中某一项完全相等（大小写敏感）的路径，其余可删项保留并标 not_confirmed；传 []：一个都不删。请原样回传预览接口给的 path', example: '["ops/clean.sh"]' },
        ],
        responseExample: JSON.stringify({
          '不带 delete_scripts（与旧版逐字节相同）': { count: 2, message: '批量delete: 2 个任务' },
          'action=delete 且 delete_scripts=true': {
            count: 2,
            message: '批量delete: 2 个任务',
            scripts: {
              tasks: [
                { id: 12, name: '京东签到', found: true, running: false, script_status: 'resolved', script_path: 'jd/sign.js', note: '' },
                { id: 13, name: '清理临时文件', found: true, running: false, script_status: 'resolved', script_path: 'ops/clean.sh', note: '' },
              ],
              deleted: [{ path: 'ops/clean.sh', task_ids: [13], deletable: true, reason: '', detail: '', shared_by: [], warnings: [] }],
              skipped: [{ path: 'jd/sign.js', task_ids: [12], deletable: false, reason: 'shared', detail: '仍被 1 个其他任务使用：京东签到（备用），删除后它会无法运行。', shared_by: [{ id: 31, name: '京东签到（备用）', text_match: false }], warnings: ['文件名 sign 常见于公共库（如 sendNotify、utils、sign），可能被其他脚本引用，确认不再需要再删。'] }],
            },
          },
        }, null, 2),
        responseFields: [
          { name: 'count', type: 'integer', description: '实际处理的任务数：只计查到的任务，不存在的 ID 不计；enable 校验不通过、stop 一个没在运行的任务、run 启动失败（RunNow 报错）的也不计。注意：run 对已经在运行中的任务不会重复触发，但仍计入 count；action 不在取值列表里时什么都不做，查到的任务同样计入 count（见 action）。' },
          { name: 'message', type: 'string', description: '「批量{action}: {count} 个任务」，例如「批量delete: 2 个任务」' },
          { name: 'scripts', type: 'object', description: '只有 action=delete 且 delete_scripts=true 时才出现，字段同 DELETE /api/tasks/:id 的 scripts；脚本只针对真实存在的任务处理' },
          { name: '不带参数时', type: 'tip', description: '不带 delete_scripts（或它为 false）时，响应与旧版逐字节一致，脚本文件原样保留。带开关时 count 与 message 的口径也不变，只是多一个 scripts 字段' },
        ],
      },
      {
        id: 'tasks-batch-delete',
        method: 'DELETE',
        path: '/api/tasks/batch/delete',
        title: '批量删除任务',
        description: '批量删除任务，连同它们的执行日志，需要 operator 及以上角色（APP 的批量删除走这里；Web 走 PUT /api/tasks/batch 的 action=delete）。注意这是一个带 JSON 请求体的 DELETE。正在运行的这次执行不会被中断。带上 delete_scripts=true 可同时删除这些任务的脚本，判定规则与 DELETE /api/tasks/:id 的 delete_script 完全一致（建议先调 POST /api/tasks/delete-preview）。用 Open API 应用令牌带 delete_scripts 时，scopes 还必须包含 scripts（或 *），否则返回 403「应用令牌缺少 scripts 权限，不能同时删除脚本」，任务和脚本都不会动。',
        auth: 'jwt',
        bodyParams: [
          { name: 'task_ids', type: 'array', required: true, description: '任务 ID 数组；传 [] 合法，得到 count=0', example: '[12,13]' },
          { name: 'delete_scripts', type: 'boolean', description: '是否同时删除脚本。必须是 JSON bool：传成字符串 "true" 会让整个请求绑定失败，返回 400「请求参数错误」，任务也不会被删', example: 'true' },
          { name: 'confirm_script_paths', type: 'array', description: '确认要删除的脚本路径列表，只能收窄范围，语义同 PUT /api/tasks/batch：不传或 null 不收窄，传 [] 一个都不删', example: '["ops/clean.sh"]' },
        ],
        responseExample: JSON.stringify({
          '不带 delete_scripts（与旧版逐字节相同）': { count: 2, message: '已删除 2 个任务' },
          '带 delete_scripts=true': {
            count: 2,
            message: '已删除 2 个任务',
            scripts: {
              tasks: [
                { id: 12, name: '京东签到', found: true, running: false, script_status: 'resolved', script_path: 'jd/sign.js', note: '' },
                { id: 13, name: '清理临时文件', found: true, running: false, script_status: 'resolved', script_path: 'ops/clean.sh', note: '' },
              ],
              deleted: [
                { path: 'jd/sign.js', task_ids: [12], deletable: true, reason: '', detail: '', shared_by: [], warnings: ['文件名 sign 常见于公共库（如 sendNotify、utils、sign），可能被其他脚本引用，确认不再需要再删。'] },
                { path: 'ops/clean.sh', task_ids: [13], deletable: true, reason: '', detail: '', shared_by: [], warnings: [] },
              ],
              skipped: [],
            },
          },
        }, null, 2),
        responseFields: [
          { name: 'count', type: 'integer', description: '等于传入 task_ids 的个数，不存在的、重复的 ID 也计入。这是现有行为，与 PUT /api/tasks/batch「只计查到的任务」的口径不同，保留不改；脚本只针对真实存在的任务处理' },
          { name: 'message', type: 'string', description: '「已删除 {count} 个任务」' },
          { name: 'scripts', type: 'object', description: '只有 delete_scripts=true 时才出现，字段同 DELETE /api/tasks/:id 的 scripts' },
          { name: '不带参数时', type: 'tip', description: '不带 delete_scripts（或它为 false）时，响应与旧版逐字节一致，脚本文件原样保留' },
        ],
      },
      {
        id: 'tasks-run',
        method: 'PUT',
        path: '/api/tasks/:id/run',
        title: '立即执行任务',
        description: '立即触发执行指定任务',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        responseExample: JSON.stringify({ message: '任务已开始执行' }, null, 2),
      },
      {
        id: 'tasks-stop',
        method: 'PUT',
        path: '/api/tasks/:id/stop',
        title: '停止任务',
        description: '停止正在执行的任务',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        responseExample: JSON.stringify({ message: '任务已停止' }, null, 2),
      },
      {
        id: 'tasks-sort',
        method: 'PUT',
        path: '/api/tasks/sort',
        title: '调整任务列表顺序',
        description: '任务列表页的拖拽排序，需要 operator 及以上角色。把 source_id 挪到 target_id 前面（position=after 时挪到后面）；不传 target_id 表示移到本区末尾。这里改的是 list_order（列表展示顺序），不是 sort_order（开机运行任务的执行顺序），两者互不影响。只允许同一「区」内互拖：区 = 置顶状态相同 且 状态分组相同（启用/运行中/排队中算一组、禁用一组），跨区返回 400「置顶任务与普通任务、启用与禁用任务请分别排序，跨区移动请用置顶 / 启用按钮」。兄弟任务取的是整个区（不是当前页），所以跨页拖拽同样有效；一次调用会把该区整体按 10 的步长重编号，其它区的值不动。注意 GET /api/tasks 带了 sort_rules 时，区的划分不变（排序规则同样只在分区内生效），但区内顺序由排序规则决定，list_order 只在规则全平手时才兜底，此时拖拽多半看不出效果。唯一的例外是 sort_rules 第一条规则 field = "status"：那种情况下列表侧的状态分区不生效（详见 GET /api/tasks 的 sort_rules 说明），但本接口的「区」定义不受影响 —— 能不能拖仍然按置顶 + 状态分组判定。',
        auth: 'jwt',
        bodyParams: [
          { name: 'source_id', type: 'integer', required: true, description: '被拖动的任务 ID', example: '12' },
          { name: 'target_id', type: 'integer', description: '落点任务 ID；不传表示移到本区末尾', example: '34' },
          { name: 'position', type: 'string', description: '相对落点的位置，只认 after（插到目标之后），其余一律按 before', example: 'before' },
        ],
        responseExample: JSON.stringify({ message: '排序更新成功' }, null, 2),
      },
      {
        id: 'task-views-list',
        method: 'GET',
        path: '/api/tasks/views',
        title: '获取任务视图列表',
        description: '任务视图就是任务页顶部那排可点的页签，每个视图带一组筛选与排序规则。视图本身只是规则的容器，真正生效是把它的 filters / sort_rules 原样作为 query 参数传给 GET /api/tasks。按 sort_order 升序返回，hidden=true 的视图仍会返回，由调用方决定要不要渲染。注意 task_views 表没有 user_id，视图是全站共享的，不是「我的视图」。',
        auth: 'jwt',
        responseExample: JSON.stringify([
          { id: 1, name: '京东', filters: '[{"field":"command","operator":"contains","value":"jd"}]', sort_rules: '[]', hidden: false, sort_order: 1 },
        ], null, 2),
      },
      {
        id: 'task-views-create',
        method: 'POST',
        path: '/api/tasks/views',
        title: '创建任务视图',
        description: '创建一个任务视图，需要 operator 及以上角色。name 是唯一索引，重名返回 400「同名任务视图已存在」。filters / sort_rules 传的是 JSON 字符串（不是数组），服务端不校验它们的 JSON 合法性——写坏了不会报错，只会在读列表时静默降级成「不筛选」，即点了这个视图却出来全部任务。不传 sort_order（或传 0）时自动排到末尾。',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '视图名称，全局唯一', example: '京东' },
          { name: 'filters', type: 'string', description: '筛选规则的 JSON 字符串，字段与取值同 GET /api/tasks 的 filters；留空按 "[]" 处理', example: '[{"field":"command","operator":"contains","value":"jd"}]' },
          { name: 'sort_rules', type: 'string', description: '排序规则的 JSON 字符串；留空按 "[]" 处理', example: '[]' },
          { name: 'sort_order', type: 'integer', description: '排序值，不传或传 0 时自动取当前最大值 +1', example: '0' },
        ],
        responseExample: JSON.stringify({ id: 1, name: '京东', filters: '[]', sort_rules: '[]', hidden: false, sort_order: 1 }, null, 2),
      },
      {
        id: 'task-views-update',
        method: 'PUT',
        path: '/api/tasks/views/:viewId',
        title: '更新任务视图',
        description: '更新指定视图，需要 operator 及以上角色。所有字段都可选，只改传了的那些。注意 name / filters / sort_rules 传空字符串等于「不修改」，所以要清空规则必须传 "[]" 而不是 ""。改名撞上别的视图返回 400「同名任务视图已存在」，视图不存在返回 404「视图不存在」。',
        auth: 'jwt',
        pathParams: [{ name: 'viewId', type: 'integer', required: true, description: '视图 ID' }],
        bodyParams: [
          { name: 'name', type: 'string', description: '视图名称；传空串等于不修改' },
          { name: 'filters', type: 'string', description: '筛选规则的 JSON 字符串；传空串等于不修改，清空要传 "[]"' },
          { name: 'sort_rules', type: 'string', description: '排序规则的 JSON 字符串；传空串等于不修改，清空要传 "[]"' },
          { name: 'hidden', type: 'boolean', description: '是否在页签条里隐藏' },
          { name: 'sort_order', type: 'integer', description: '排序值' },
        ],
        responseExample: JSON.stringify({ id: 1, name: '京东', filters: '[]', sort_rules: '[]', hidden: false, sort_order: 1 }, null, 2),
      },
      {
        id: 'task-views-delete',
        method: 'DELETE',
        path: '/api/tasks/views/:viewId',
        title: '删除任务视图',
        description: '删除指定视图，需要 operator 及以上角色。只删规则容器，不动任何任务。视图不存在返回 404「视图不存在」。',
        auth: 'jwt',
        pathParams: [{ name: 'viewId', type: 'integer', required: true, description: '视图 ID' }],
        responseExample: JSON.stringify({ message: '已删除' }, null, 2),
      },
      {
        id: 'task-views-reorder',
        method: 'PUT',
        path: '/api/tasks/views/reorder',
        title: '批量调整视图顺序',
        description: '在一个事务里批量更新视图的 sort_order 与 hidden，需要 operator 及以上角色。payload 里没出现的视图原样不动，所以整份提交和只提交一部分都可以。views 为空数组时直接返回 {"updated": 0}。',
        auth: 'jwt',
        bodyParams: [
          { name: 'views', type: 'array', required: true, description: '[{id, sort_order, hidden?}]，id 必填', example: '[{"id":1,"sort_order":2},{"id":2,"sort_order":1,"hidden":true}]' },
        ],
        responseExample: JSON.stringify({
          updated: 2,
          views: [{ id: 2, name: '订阅', filters: '[]', sort_rules: '[]', hidden: true, sort_order: 1 }],
        }, null, 2),
      },
    ],
  },
  {
    key: 'logs',
    label: '执行日志',
    endpoints: [
      {
        id: 'logs-list',
        method: 'GET',
        path: '/api/logs',
        title: '获取日志列表',
        description: '获取任务执行日志，支持按任务、状态过滤和分页',
        auth: 'jwt',
        queryParams: [
          { name: 'task_id', type: 'integer', description: '按任务 ID 过滤', example: '1' },
          { name: 'status', type: 'integer', description: '状态过滤：0=成功, 1=失败, 2=运行中, 3=已终止' },
          { name: 'keyword', type: 'string', description: '按任务名称模糊匹配（子串包含，不是通配符）', example: '签到' },
          { name: 'page', type: 'integer', description: '页码', example: '1' },
          { name: 'page_size', type: 'integer', description: '每页数量，最大 100，超出或非法时按 20 处理', example: '20' },
        ],
        responseExample: JSON.stringify({
          data: [{ id: 1, task_id: 1, task_name: '签到任务', status: 0, content: '执行成功', duration: 2.5, started_at: '2026-03-10T09:00:00' }],
          total: 1,
          page: 1,
          page_size: 20,
        }, null, 2),
      },
      {
        id: 'logs-detail',
        method: 'GET',
        path: '/api/logs/:id',
        title: '获取日志详情',
        description: '获取单条日志的详细内容。content 返回的是按终端语义折叠过裸 \\r（回车覆盖）之后的文本，进度条之类的刷新输出会被折叠成最终一帧；需要磁盘上一模一样的原始字节，请改用下面的「原始日志下载」两步接口。log_path 为该日志在日志目录内的相对路径，为 null 表示这条日志的内容只存在数据库里、没有独立的日志文件。task_name / command / task_type / labels 这几个字段来自关联的任务，任务已被删除时不会出现，调用方要按可空处理。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '日志 ID' }],
        responseExample: JSON.stringify({
          id: 1, task_id: 1, task_name: '签到任务', command: 'task sign.py', status: 0, content: '签到成功\n获得 10 积分', duration: 2.5, log_path: 'task_1_签到/2026-03-10-09-00-00.log', started_at: '2026-03-10T09:00:00', ended_at: '2026-03-10T09:00:02',
        }, null, 2),
      },
      {
        id: 'logs-raw-ticket',
        method: 'GET',
        path: '/api/logs/:id/raw-ticket',
        title: '签发原始日志下载票据',
        description: '下载原始日志分两步，这是第一步「换票」，不能跳过它直接调 /raw。面板其它日志接口给出的都是按终端语义折叠过裸 \\r 的文本，只有这条链路能拿到磁盘文件的原始字节（含未被折叠的终端控制序列）。本接口的鉴权与 GET /api/logs/:id 完全一致：走正常的 Authorization: Bearer（用户 JWT 或带 logs 权限的 Open API Token，viewer 及以上角色）。校验通过并在磁盘上定位到文件后，签发一张有效期 120 秒、且只对这一条日志生效的票据，返回体里的 url 已经把 ticket 拼好，是一个相对路径，直接接在面板地址后面即可，不要自行改写其中的参数。失败情况：日志 ID 不是数字返回 400「无效的日志ID」；日志记录不存在返回 404「日志不存在」；日志内容只存在数据库里（log_path 为空，这类记录的内容会被压缩后直接写进 content 字段）时没有原始文件可下，返回 404「该日志没有独立的原始日志文件（内容仅存于数据库）」；文件已被清理或路径异常返回 404「原始日志文件不存在或已被清理」；签名失败返回 500「签发下载票据失败」。错误响应体统一为 {"error": "..."}。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '日志 ID' }],
        responseExample: JSON.stringify({
          url: '/api/logs/12/raw?ticket=v1.YWRtaW4.1785000120.3Qk8x1r0Zq2m9v7bN4pS6cW8dY0aE2gH5jK7lM9nQ1s',
          filename: '签到任务-12-raw.log',
          size: 20480,
          expires_at: '2026-08-04T12:02:00+08:00',
          expires_in: 120,
        }, null, 2),
        responseFields: [
          { name: 'url', type: 'string', description: '已拼好票据的下载地址（相对路径），直接接在面板地址后访问即可；请整段照用，改写任何参数都会导致验签失败' },
          { name: 'filename', type: 'string', description: '建议保存的文件名，格式为 <任务名>-<日志ID>-raw.log；-raw 后缀用于和前端「折叠后」的下载区分' },
          { name: 'size', type: 'integer', description: '磁盘文件字节数' },
          { name: 'expires_at', type: 'string', description: '票据过期时间（RFC3339）' },
          { name: 'expires_in', type: 'integer', description: '票据有效期（秒），当前为 120' },
        ],
      },
      {
        id: 'logs-raw',
        method: 'GET',
        path: '/api/logs/:id/raw',
        title: '下载原始日志文件',
        description: '下载原始日志的第二步：凭票据直传文件。这条路由刻意没有挂 JWT 中间件，只认 URL 上的 ticket —— 因为浏览器原生下载（<a download> / window.open）带不上 Authorization 头；反过来也成立：只带 Bearer 而不带 ticket 一样会被拒（401「缺少下载票据」）。票据由上一步的 raw-ticket 签发，HMAC 签名里绑定了具体的日志 ID，挪到另一条日志上用会 401「下载票据无效」；超过 120 秒返回 401「下载票据已过期，请重新发起下载」。票据不落库、不是一次性的，有效期内可重复使用，且只在请求开始时校验一次，传输本身持续多久都不受影响（浏览器断线重试 / Range 续传因此不会失败）。响应是文件流而不是 JSON：Content-Type 为 text/plain; charset=utf-8，服务端按 32KB 分块直传不整体读进内存，并支持 Range 续传；Content-Disposition 同时给出 ASCII 回退名 filename="..." 和 RFC 5987 的 filename*=UTF-8\'\'...（中文文件名不会乱码）。日志被清理时返回 404「原始日志文件不存在或已被清理」。',
        auth: 'ticket',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '日志 ID，必须与换票时的一致' }],
        queryParams: [
          { name: 'ticket', type: 'string', required: true, description: '上一步 raw-ticket 签发的票据（已包含在返回的 url 里，正常无需手动拼）', example: 'v1.YWRtaW4.1785000120.3Qk8...' },
        ],
        responseContentType: 'text/plain; charset=utf-8（文件流）',
        responseExample: `HTTP/1.1 200 OK
Content-Type: text/plain; charset=utf-8
Content-Length: 20480
Content-Disposition: attachment; filename="____-12-raw.log"; filename*=UTF-8''%E7%AD%BE%E5%88%B0%E4%BB%BB%E5%8A%A1-12-raw.log
Accept-Ranges: bytes
Cache-Control: no-store, no-cache, must-revalidate
X-Content-Type-Options: nosniff

<磁盘日志文件的原始字节，保留裸 \\r 等终端控制序列，未做任何折叠处理>`,
      },
      {
        id: 'tasks-log-file-raw-ticket',
        method: 'GET',
        path: '/api/tasks/:id/log-files/:filename/raw-ticket',
        title: '签发任务日志文件下载票据',
        description: '任务日志文件（任务详情里「日志文件」列表中的 .log）原始内容下载的第一步：换票，同样不能跳过它直接调 /raw。鉴权与 GET /api/tasks/:id/log-files/:filename 完全一致（Authorization: Bearer + viewer 及以上角色）。文件定位口径也与日志文件预览 / 删除 / 下载一致：优先取查询参数 path（日志目录内的相对路径），没传或为空时回落到路径参数 filename。票据有效期 120 秒，并绑定「任务 ID + 本次传入的定位参数」，换不到别的任务或别的文件上去用。签发时传了 path，返回的 url 会把它原样带回，请整段照用、不要自己拼。失败情况：任务 ID 不是数字返回 400「无效的任务ID」；文件不存在、首段目录不是该任务的 task_<id>[_label] 目录、或存在路径穿越（../、绝对路径、软链逃逸）一律返回 404「原始日志文件不存在或已被清理」；签名失败返回 500「签发下载票据失败」。',
        auth: 'jwt',
        pathParams: [
          { name: 'id', type: 'integer', required: true, description: '任务 ID' },
          { name: 'filename', type: 'string', required: true, description: '日志文件名，需 URL 编码；传了 path 时它只作占位', example: '2026-03-10-09-00-00.log' },
        ],
        queryParams: [
          { name: 'path', type: 'string', description: '日志目录内的相对路径，首段必须是该任务的 task_<id>[_label] 目录；同名文件分散在多个任务目录时用它精确定位', example: 'task_1_签到/2026-03-10-09-00-00.log' },
        ],
        responseExample: JSON.stringify({
          url: '/api/tasks/1/log-files/2026-03-10-09-00-00.log/raw?path=task_1_%E7%AD%BE%E5%88%B0%2F2026-03-10-09-00-00.log&ticket=v1.YWRtaW4.1785000120.3Qk8x1r0Zq2m9v7bN4pS6cW8dY0aE2gH5jK7lM9nQ1s',
          filename: '2026-03-10-09-00-00.log',
          size: 20480,
          expires_at: '2026-08-04T12:02:00+08:00',
          expires_in: 120,
        }, null, 2),
        responseFields: [
          { name: 'url', type: 'string', description: '已拼好 path 与 ticket 的下载地址（相对路径）；path 必须与换票时完全一致，所以请整段照用' },
          { name: 'filename', type: 'string', description: '建议保存的文件名，取相对路径的最后一段' },
          { name: 'size', type: 'integer', description: '磁盘文件字节数' },
          { name: 'expires_at', type: 'string', description: '票据过期时间（RFC3339）' },
          { name: 'expires_in', type: 'integer', description: '票据有效期（秒），当前为 120' },
        ],
      },
      {
        id: 'tasks-log-file-raw',
        method: 'GET',
        path: '/api/tasks/:id/log-files/:filename/raw',
        title: '下载任务日志文件原始内容',
        description: '任务日志文件原始内容下载的第二步：凭票据直传。与 /logs/:id/raw 一样，这条路由没有挂 JWT 中间件、只认 URL 上的 ticket，带 Bearer 而不带 ticket 会被拒（401「缺少下载票据」）。票据绑定的是「任务 ID + 原样的定位参数」，所以 path 必须和换票时一模一样（直接用返回的 url 最稳）：改了 path、换了任务、或者试图用 ../ 穿越，都会先在验签这一关被拒（401「下载票据无效」），根本碰不到磁盘；票据过期返回 401「下载票据已过期，请重新发起下载」。有效期内票据可重复使用，只在请求开始时校验一次。响应是文件流而不是 JSON：Content-Type 为 text/plain; charset=utf-8，支持 Range 续传，Content-Disposition 同时给出 ASCII 回退名和 RFC 5987 的 filename*=UTF-8\'\'...；文件内容保留裸 \\r 等终端控制序列，不做任何折叠。文件已被清理时返回 404「原始日志文件不存在或已被清理」。',
        auth: 'ticket',
        pathParams: [
          { name: 'id', type: 'integer', required: true, description: '任务 ID，必须与换票时的一致' },
          { name: 'filename', type: 'string', required: true, description: '日志文件名（URL 编码）；传了 path 时它只作占位' },
        ],
        queryParams: [
          { name: 'ticket', type: 'string', required: true, description: '上一步 raw-ticket 签发的票据（已包含在返回的 url 里）', example: 'v1.YWRtaW4.1785000120.3Qk8...' },
          { name: 'path', type: 'string', description: '必须与换票时传入的值完全一致，否则验签失败；换票时没传就不要传', example: 'task_1_签到/2026-03-10-09-00-00.log' },
        ],
        responseContentType: 'text/plain; charset=utf-8（文件流）',
        responseExample: `HTTP/1.1 200 OK
Content-Type: text/plain; charset=utf-8
Content-Length: 20480
Content-Disposition: attachment; filename="2026-03-10-09-00-00.log"; filename*=UTF-8''2026-03-10-09-00-00.log
Accept-Ranges: bytes
Cache-Control: no-store, no-cache, must-revalidate
X-Content-Type-Options: nosniff

<磁盘日志文件的原始字节，保留裸 \\r 等终端控制序列，未做任何折叠处理>`,
      },
      {
        id: 'tasks-log-archive-ticket',
        method: 'GET',
        path: '/api/tasks/:id/log-files/archive-ticket',
        title: '签发任务日志打包下载票据',
        description: '把一个任务的日志文件夹一次性打成 zip 下载，分两步，这是第一步「换票」。适用于「一个每 5 分钟跑一次的任务，7 天有 2000+ 个日志文件，逐个下载点不完」的场景。待打包的文件清单直接复用 GET /api/tasks/:id/log-files 那份数据，所以「下载到的 = 列表里看到的」。本接口的鉴权与其它日志文件接口完全一致：Authorization: Bearer（用户 JWT 或带 tasks 权限的 Open API Token，viewer 及以上角色）。收集与上限校验全部在签票时完成（zip 一旦开始写就没法回退成 JSON 错误），返回体里的 url 已经把 ticket 与 start/end 拼好，是一个相对路径，直接接在面板地址后面即可，不要自行改写其中的参数 —— start/end 参与票据签名，改任何一个都会导致下载时 401。失败情况：任务 ID 不是数字返回 400「无效的任务ID」；该任务（在给定时间范围内）一个日志文件都没有返回 404「该任务暂无日志文件」；超过 2000 个文件或未压缩总量超过 512MB 返回 400，文案里会带上实际数量与体积并提示改用更小的时间范围；签名失败返回 500「签发下载票据失败」。错误响应体统一为 {"error": "..."}。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        queryParams: [
          { name: 'start', type: 'string', description: '起始时间（RFC3339），按日志文件的磁盘修改时间筛选，也就是日志文件列表里 created_at 那一列；不传表示不限起点', example: '2026-03-04T00:00:00+08:00' },
          { name: 'end', type: 'string', description: '结束时间（RFC3339），闭区间；不传表示不限终点。手写 URL 时请把时区里的 + 编码成 %2B，否则会被解析成空格（服务端有容错，但不要依赖）', example: '2026-03-10T23:59:59+08:00' },
        ],
        responseExample: JSON.stringify({
          url: '/api/tasks/1/log-files/archive?end=2026-03-10T23%3A59%3A59%2B08%3A00&start=2026-03-04T00%3A00%3A00%2B08%3A00&ticket=v1.YWRtaW4.1785000120.3Qk8x1r0Zq2m9v7bN4pS6cW8dY0aE2gH5jK7lM9nQ1s',
          filename: '签到任务-logs-20260310.zip',
          size: 20971520,
          file_count: 168,
          expires_at: '2026-08-04T12:02:00+08:00',
          expires_in: 120,
        }, null, 2),
        responseFields: [
          { name: 'url', type: 'string', description: '已拼好 start / end / ticket 的下载地址（相对路径）；三个参数都参与验签，请整段照用' },
          { name: 'filename', type: 'string', description: '建议保存的文件名，格式为 <任务名>-logs-<YYYYMMDD>.zip' },
          { name: 'size', type: 'integer', description: '本次要打包的【未压缩】总字节数。流式 zip 事先算不出压缩后大小，所以这不是响应体长度' },
          { name: 'file_count', type: 'integer', description: '本次要打包的文件个数' },
          { name: 'expires_at', type: 'string', description: '票据过期时间（RFC3339）' },
          { name: 'expires_in', type: 'integer', description: '票据有效期（秒），当前为 120' },
        ],
      },
      {
        id: 'tasks-log-archive',
        method: 'GET',
        path: '/api/tasks/:id/log-files/archive',
        title: '下载任务日志压缩包',
        description: '打包下载的第二步：凭票据直传 zip。这条路由刻意没有挂 JWT 中间件，只认 URL 上的 ticket —— 因为浏览器原生下载带不上 Authorization 头；反过来也成立：只带 Bearer 而不带 ticket 一样会被拒（401「缺少下载票据」）。票据的 HMAC 签名里绑定了任务 ID 与原样的 start/end，挪到另一个任务或改动时间范围都会 401「下载票据无效」；超过 120 秒返回 401「下载票据已过期，请重新发起下载」。验票通过后会把文件清单与上限校验完整重跑一遍（票据有效期内文件可能已被日志清理删掉），再开始写 zip。zip 内的路径就是日志根目录内的相对路径（形如 task_1_签到/2026-03-10-09-00-00.log），任务改过名留下多个 task_<id>_<名字> 目录时层级会完整保留、不会撞名；文件内容是磁盘原始字节，未做任何折叠处理。响应是边压边发的流：没有 Content-Length，也【不支持 Range 断点续传】（不会返回 Accept-Ranges），断线只能整包重来。Content-Disposition 同时给出 ASCII 回退名 filename="..." 和 RFC 5987 的 filename*=UTF-8\'\'...（中文任务名不会乱码）。',
        auth: 'ticket',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID，必须与换票时的一致' }],
        queryParams: [
          { name: 'ticket', type: 'string', required: true, description: '上一步 archive-ticket 签发的票据（已包含在返回的 url 里，正常无需手动拼）', example: 'v1.YWRtaW4.1785000120.3Qk8...' },
          { name: 'start', type: 'string', description: '必须与换票时传入的值完全一致，否则验签失败；换票时没传就不要传', example: '2026-03-04T00:00:00+08:00' },
          { name: 'end', type: 'string', description: '必须与换票时传入的值完全一致，否则验签失败；换票时没传就不要传', example: '2026-03-10T23:59:59+08:00' },
        ],
        responseContentType: 'application/zip（流式压缩包）',
        responseExample: `HTTP/1.1 200 OK
Content-Type: application/zip
Content-Disposition: attachment; filename="____-logs-20260310.zip"; filename*=UTF-8''%E7%AD%BE%E5%88%B0%E4%BB%BB%E5%8A%A1-logs-20260310.zip
Cache-Control: no-store
X-Content-Type-Options: nosniff
Transfer-Encoding: chunked

<zip 字节流；每个 entry 的路径为 task_<id>[_<任务名>]/<文件名>.log，内容与磁盘完全一致>`,
      },
      {
        id: 'logs-delete',
        method: 'DELETE',
        path: '/api/logs/:id',
        title: '删除日志',
        description: '删除指定日志记录。日志不存在时返回 404「日志不存在」',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '日志 ID' }],
        responseExample: JSON.stringify({ message: '日志已删除' }, null, 2),
      },
    ],
  },
  {
    key: 'scripts',
    label: '脚本管理',
    endpoints: [
      {
        id: 'scripts-list',
        method: 'GET',
        path: '/api/scripts',
        title: '获取脚本列表',
        description: '获取所有脚本文件列表',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ path: 'sign.py', size: 1024, modified: '2026-03-10T00:00:00' }] }, null, 2),
      },
      {
        id: 'scripts-content',
        method: 'GET',
        path: '/api/scripts/content?path=:path',
        title: '获取脚本内容',
        description: '获取指定脚本的文件内容',
        auth: 'jwt',
        queryParams: [{ name: 'path', type: 'string', required: true, description: '脚本路径', example: 'sign.py' }],
        responseExample: JSON.stringify({ data: { path: 'sign.py', content: 'print("hello")', binary: false, is_binary: false } }, null, 2),
      },
      {
        id: 'scripts-save',
        method: 'PUT',
        path: '/api/scripts/content',
        title: '保存脚本内容',
        description: '创建或更新脚本文件',
        auth: 'jwt',
        bodyParams: [
          { name: 'path', type: 'string', required: true, description: '脚本路径' },
          { name: 'content', type: 'string', required: true, description: '脚本内容' },
        ],
        responseExample: JSON.stringify({ message: '保存成功' }, null, 2),
      },
      {
        id: 'scripts-delete',
        method: 'DELETE',
        path: '/api/scripts/:path',
        title: '删除脚本',
        description: '删除指定脚本文件',
        auth: 'jwt',
        pathParams: [{ name: 'path', type: 'string', required: true, description: '脚本路径' }],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
      {
        id: 'scripts-upload',
        method: 'POST',
        path: '/api/scripts/upload',
        title: '上传脚本',
        description: '上传脚本文件，支持 .py/.js/.sh/.ts',
        auth: 'jwt',
        bodyParams: [{ name: 'file', type: 'file', required: true, description: '脚本文件（multipart/form-data）' }],
        responseExample: JSON.stringify({ message: '上传成功', data: { path: 'sign.py' } }, null, 2),
      },
    ],
  },
  {
    key: 'envs',
    label: '环境变量',
    endpoints: [
      {
        id: 'envs-list',
        method: 'GET',
        path: '/api/envs',
        title: '获取所有环境变量',
        description: '获取环境变量列表，支持 keyword 模糊搜索与 names / groups / enabled 精确筛选（各条件之间是 AND），支持分页；带 all=1 时一次返回全部（硬上限 5000）',
        auth: 'jwt',
        queryParams: [
          { name: 'keyword', type: 'string', description: '搜索关键字：对 变量名 / 变量值 / 备注 / 分组 四个字段做大小写不敏感的模糊匹配（UPPER LIKE %值%），四个字段之间是 OR、无法限定到某一个字段。要按变量名精确匹配请改用 names', example: 'JD_COOKIE' },
          { name: 'names', type: 'string', description: '按变量名【精确】筛选（name IN (...)），不是 keyword 那种模糊匹配——传 JD_COOKIE 不会把 JD_COOKIE_EXTRA 一起带出来。支持 names=A,B 逗号分隔（中文逗号与分号同样认），也支持重复参数 names=A&names=B，两种写法等价合并并去重；多个变量名之间是 OR，与 keyword / groups / enabled 之间是 AND。传库里不存在的名字就返回空列表，不会回落成「不筛」。可选值由 GET /api/envs/names 提供', example: 'JD_COOKIE,PT_KEY' },
          { name: 'groups', type: 'string', description: '按分组筛选，分组是【整段精确】匹配而不是模糊匹配（传「京东」不会命中「京东备用」）。支持 groups=A,B 逗号分隔，也支持重复参数 groups=A&groups=B；多个分组之间是 OR，与其它筛选之间是 AND。一条变量可以同时属于多个分组，命中任意一个即算命中。可选值由 GET /api/envs/groups 提供', example: '京东,美团' },
          { name: 'group', type: 'string', description: 'groups 的单数别名，语义完全一致。两者同时传时是【合并去重后一起生效】，不是互相覆盖', example: '京东' },
          { name: 'enabled', type: 'boolean', description: '按启用状态筛选：true 只看已启用、false 只看已禁用，不传表示不筛。取值按 Go 的 strconv.ParseBool 解析（1 / t / T / true / TRUE / 0 / f / F / false / FALSE 都认），解析失败时【静默忽略】这个条件、不报 400', example: 'true' },
          { name: 'page', type: 'integer', description: '页码，默认 1；小于 1 一律按 1 处理', example: '1' },
          { name: 'page_size', type: 'integer', description: '每页数量，默认 20；小于 1 或大于 100 时【一律回落成 20】，不是截断到 100。带 all=1 时本参数被忽略', example: '20' },
          { name: 'all', type: 'integer', description: '设为 1（也认 true / yes）时一次返回全部（最多 5000 条），忽略 page / page_size；keyword / names / groups / enabled 等筛选条件仍然生效', example: '1' },
        ],
        responseExample: JSON.stringify({ data: [{ id: 1, name: 'MY_TOKEN', value: 'abc123', enabled: true, remarks: '签到Token' }], total: 1 }, null, 2),
        responseFields: [
          { name: 'id', type: 'integer', description: '变量 ID' },
          { name: 'name', type: 'string', description: '变量名' },
          { name: 'value', type: 'string', description: '变量值' },
          { name: 'enabled', type: 'boolean', description: '是否启用' },
          { name: 'remarks', type: 'string', description: '备注' },
        ],
      },
      {
        id: 'envs-names',
        method: 'GET',
        path: '/api/envs/names',
        title: '获取全部变量名',
        description: '按变量名聚合，返回去重后的变量名与各自的条数，按 name 升序。用途是给「变量名筛选」下拉提供选项：拿到的 name 可以原样塞进 GET /api/envs 的 names 参数。⚠️ count 的口径是【全库同名条数】，刻意不跟随 keyword / groups / enabled 等筛选——它与 PUT /api/envs/by-name 冲突时 409 文案里那句「存在 N 条名为 X 的环境变量」是同一个 N，两处必须对得上；若改成筛选后的条数，同一个变量名会在下拉里和报错里显示两个不同的数字。同名多条是刻意支持的功能（一个变量名下挂多个账号），不是脏数据，所以 count > 1 很常见。空名（name 为空串）不计入；库里一条都没有时返回空数组 [] 而不是 null。鉴权与 GET /api/envs/groups 完全同组。',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ name: 'JD_COOKIE', count: 3 }, { name: 'PT_KEY', count: 1 }] }, null, 2),
        responseFields: [
          { name: 'data', type: 'array', description: '变量名列表，按 name 升序；库里没有变量时为空数组' },
          { name: 'data[].name', type: 'string', description: '变量名（已去重）' },
          { name: 'data[].count', type: 'integer', description: '全库中同名环境变量的条数，不随任何筛选变化' },
        ],
      },
      {
        id: 'envs-get',
        method: 'GET',
        path: '/api/envs/:id',
        title: '按 ID 获取环境变量',
        description: '根据变量 ID 直接返回单条环境变量详情，避免按 keyword 反复查询匹配。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '变量 ID' }],
        responseExample: JSON.stringify({ data: { id: 1, name: 'MY_TOKEN', value: 'abc123', enabled: true, remarks: '签到Token', group: '京东' } }, null, 2),
      },
      {
        id: 'envs-create',
        method: 'POST',
        path: '/api/envs',
        title: '创建环境变量',
        description: '创建新的环境变量',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '变量名', example: 'MY_TOKEN' },
          { name: 'value', type: 'string', required: true, description: '变量值', example: 'abc123' },
          { name: 'remarks', type: 'string', description: '备注' },
          { name: 'group', type: 'string', description: '分组名称' },
        ],
        responseExample: JSON.stringify({ message: '创建成功', data: { id: 1 } }, null, 2),
      },
      {
        id: 'envs-update',
        method: 'PUT',
        path: '/api/envs/:id',
        title: '更新环境变量',
        description: '更新指定环境变量',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '变量 ID' }],
        bodyParams: [
          { name: 'name', type: 'string', description: '变量名' },
          { name: 'value', type: 'string', description: '变量值' },
          { name: 'remarks', type: 'string', description: '备注' },
          { name: 'group', type: 'string', description: '分组名称' },
        ],
        responseExample: JSON.stringify({ message: '更新成功' }, null, 2),
      },
      {
        id: 'envs-delete',
        method: 'DELETE',
        path: '/api/envs/:id',
        title: '删除环境变量',
        description: '删除指定环境变量',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '变量 ID' }],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
    ],
  },
  {
    key: 'subscriptions',
    label: '订阅管理',
    endpoints: [
      {
        id: 'subs-list',
        method: 'GET',
        path: '/api/subscriptions',
        title: '获取订阅列表',
        description: '获取所有仓库订阅',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ id: 1, name: '示例仓库', url: 'https://github.com/user/repo.git', branch: 'main', schedule: '0 0 * * *', enabled: true }] }, null, 2),
      },
      {
        id: 'subs-create',
        method: 'POST',
        path: '/api/subscriptions',
        title: '创建订阅',
        description: '创建新的仓库订阅',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '订阅名称' },
          { name: 'url', type: 'string', required: true, description: '仓库 URL（HTTP/HTTPS）' },
          { name: 'branch', type: 'string', description: '分支，默认 main', example: 'main' },
          { name: 'schedule', type: 'string', description: 'Cron 表达式', example: '0 0 * * *' },
          { name: 'whitelist', type: 'string', description: '白名单：文件名/路径片段，「子串包含」匹配（非 glob、非正则），多个用 , 或 | 分隔；片段命中目录名时该目录下的全部文件（含多级子目录）都算命中。命中的文件才会检出落盘并建成定时任务；留空表示全部命中', example: 'jd_|jx_|jddj_' },
          { name: 'blacklist', type: 'string', description: '黑名单：匹配规则同白名单（片段命中目录名时整个目录都被排除）。命中的文件既不检出也不建任务', example: 'backUp' },
          { name: 'depend_on', type: 'string', description: '依赖规则：对应青龙 ql repo 的第 4 个参数 dependence，匹配规则同白名单（填 utils 会把 utils/ 目录下的文件一并检出）。命中的文件会被检出落盘供主脚本调用，但不会建成定时任务；白名单为空时本字段不生效', example: 'sendNotify|utils' },
          { name: 'sub_path', type: 'string', description: '只检出仓库内的指定子目录（多个用 , 或 | 分隔），优先级高于白名单' },
          { name: 'save_dir', type: 'string', description: '脚本存放子目录，留空则按仓库名推导' },
          { name: 'pre_script', type: 'string', description: '拉取前指令：在 git 拉取之前执行的 Shell 命令，可用 $SUB_DIR / $SCRIPTS_DIR / $QL_DIR 等变量。非 0 退出会中断本次拉取并记为失败', example: 'mount -a' },
          { name: 'hook_script', type: 'string', description: '拉取后钩子：拉取成功后、同步定时任务之前执行的 Shell 命令，变量与失败语义同 pre_script', example: 'bash $SUB_DIR/copyfiles.sh' },
          { name: 'overwrite_mode', type: 'string', description: '覆盖拉取策略，仅对 git 仓库订阅生效：inherit=跟随系统设置里的「覆盖拉取（默认）」开关（默认值）、force=强制覆盖本地改动、preserve=拉取前暂存本地改动再恢复。作用域只有脚本文件（git 工作区），不影响任务的名称与定时；首次拉取（本地还没有仓库）不适用。不传或传非法值一律按 inherit 处理', example: 'preserve' },
          { name: 'full_checkout', type: 'boolean', description: '完整检出，仅对 git 仓库订阅生效：true=放弃 sparse-checkout，拉取整个仓库（源码、资源、文档全部落盘，体积可能很大）；不传或 false=按「指定子目录 / 白名单 / 依赖规则」稀疏检出（默认）。不改变建任务的规则：仍然只有命中白名单的文件会被建成定时任务', example: 'true' },
          { name: 'auto_add_task_mode', type: 'string', description: '自动添加定时任务策略：inherit=跟随系统设置里的「自动添加定时任务（默认）」开关（默认值）、enabled=这条订阅强制建任务、disabled=这条订阅一律不建任务。作用域是拉取成功后「把命中的脚本同步成定时任务」这一步，不影响脚本文件的检出落盘；与只对 git 仓库订阅生效的 overwrite_mode / full_checkout 不同，本项对单文件订阅同样生效。不传或传非法值一律按 inherit 处理', example: 'disabled' },
          { name: 'auto_del_task_mode', type: 'string', description: '自动删除失效任务策略：inherit=跟随系统设置里的「自动删除失效任务（默认）」开关（默认值）、enabled=这条订阅强制删除源文件已消失的任务、disabled=源文件没了也保留任务。作用域只有本订阅名下的任务，不会动用户自建任务；与 overwrite_mode / full_checkout 不同，本项对单文件订阅同样生效。不传或传非法值一律按 inherit 处理', example: 'enabled' },
          { name: 'auto_add_task', type: 'boolean', description: '已废弃：仅作老客户端兼容保留，请改用 auto_add_task_mode。**这一列永远不会被写成 true** —— 创建时如果只给了这个布尔、没给三态字段，服务端会把 true 翻译成 auto_add_task_mode=enabled 再落库，本列恒为 false；同时给了合法三态时以三态为准。老库里为 true 的行会在升级时一次性迁移成 enabled 并把本列清零' },
          { name: 'auto_del_task', type: 'boolean', description: '已废弃：仅作老客户端兼容保留，请改用 auto_del_task_mode。语义同 auto_add_task' },
        ],
        responseExample: JSON.stringify({ message: '创建成功', data: { id: 1 } }, null, 2),
      },
      {
        id: 'subs-update',
        method: 'PUT',
        path: '/api/subscriptions/:id',
        title: '更新订阅',
        description: '更新指定订阅配置。请求体为部分更新，字段与「创建订阅」一致（含 whitelist / blacklist / depend_on / sub_path / overwrite_mode / full_checkout / auto_add_task_mode / auto_del_task_mode），只提交需要修改的字段即可。其中 overwrite_mode 的取值为 inherit / force / preserve，非法值会被归一成 inherit；full_checkout 为布尔值，true 表示拉取整个仓库、false（默认）表示按子目录/白名单/依赖规则稀疏检出；auto_add_task_mode / auto_del_task_mode 的取值为 inherit / enabled / disabled，非字符串或非法值同样归一成 inherit，且对单文件订阅一样生效。已废弃的 auto_add_task / auto_del_task **不在更新白名单里**，传了会被直接忽略（老客户端兼容靠的是响应里仍然下发这两个字段，不是靠可写）。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '订阅 ID' }],
        responseExample: JSON.stringify({ message: '更新成功' }, null, 2),
      },
      {
        id: 'subs-delete',
        method: 'DELETE',
        path: '/api/subscriptions/:id',
        title: '删除订阅',
        description: '删除指定订阅',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '订阅 ID' }],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
      {
        id: 'subs-pull',
        method: 'PUT',
        path: '/api/subscriptions/:id/pull',
        title: '手动拉取订阅',
        description: '立即拉取指定订阅的脚本。拉取时是否覆盖本地修改由该订阅自己的 overwrite_mode 决定（inherit 时回落到系统设置里的「覆盖拉取（默认）」开关），本接口不接收临时覆盖模式参数。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '订阅 ID' }],
        responseExample: JSON.stringify({ message: '拉取成功，拉取 5 个文件，新增 3 个任务' }, null, 2),
      },
    ],
  },
  {
    key: 'notifications',
    label: '通知渠道',
    endpoints: [
      {
        id: 'notify-list',
        method: 'GET',
        path: '/api/notifications',
        title: '获取通知渠道列表',
        description: '获取所有通知渠道配置',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ id: 1, name: '钉钉通知', type: 'dingtalk', config: { token: '***' }, push_scope: 'default', enabled: true }] }, null, 2),
      },
      {
        id: 'notify-create',
        method: 'POST',
        path: '/api/notifications',
        title: '创建通知渠道',
        description: '创建新的通知渠道，支持：webhook / email / telegram(支持 api_host、proxy 单独代理) / dingtalk / wecom(企业微信机器人，支持 text/markdown/markdown_v2/image/news/template_card) / wecom_app(企业微信应用，支持 text/markdown/image/file/video/news/mpnews/template_card，并支持 base_url 反代基础地址、proxy 正向代理（http/https/socks5，留空使用系统代理）) / bark / pushplus(支持 channel 选择发送渠道：wechat/app/extension/webhook/clawbot/cp/qq/mail/sms/voice，webhook、cp 与 qq 需配合 option 填渠道编码；QQ 发给个人时 option 留空，发给群才填群配置编码) / serverchan / feishu / gotify / pushdeer / pushme / chanify / igot / qmsg / pushover / discord / slack / ntfy / wxpusher(WxPusher / ClawBot(iLink)，支持 url、verify_pay_type) / custom',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '渠道名称' },
          { name: 'type', type: 'string', required: true, description: '渠道类型', example: 'dingtalk' },
          { name: 'config', type: 'object', required: true, description: '渠道配置（各类型字段不同，例如 email 可填 smtp_ssl，telegram 可填 proxy，wecom_app 可填 base_url / proxy，wxpusher 可填 url / verify_pay_type）' },
          { name: 'push_scope', type: 'string', description: '推送范围：default（默认推送，参与广播）/ bound（绑定推送，只有被任务绑定或调用时显式指定才推送）。留空按 default 处理', example: 'default' },
        ],
        responseExample: JSON.stringify({ message: '创建成功', data: { id: 1 } }, null, 2),
      },
      {
        id: 'notify-update',
        method: 'PUT',
        path: '/api/notifications/:id',
        title: '更新通知渠道',
        description: '更新指定通知渠道。按键更新：请求里没出现的键不会改动已有值',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '渠道 ID' }],
        bodyParams: [
          { name: 'name', type: 'string', description: '渠道名称' },
          { name: 'type', type: 'string', description: '渠道类型' },
          { name: 'config', type: 'string', description: '渠道配置 JSON 字符串' },
          { name: 'push_scope', type: 'string', description: '推送范围：default / bound。不传则保持原值', example: 'bound' },
        ],
        responseExample: JSON.stringify({ message: '更新成功' }, null, 2),
      },
      {
        id: 'notify-delete',
        method: 'DELETE',
        path: '/api/notifications/:id',
        title: '删除通知渠道',
        description: '删除指定通知渠道',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '渠道 ID' }],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
      {
        id: 'notify-test',
        method: 'POST',
        path: '/api/notifications/:id/test',
        title: '测试发送',
        description: '向指定渠道发送测试通知',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '渠道 ID' }],
        responseExample: JSON.stringify({ message: '测试通知发送成功' }, null, 2),
      },
      {
        id: 'notify-send',
        method: 'POST',
        path: '/api/notifications/send',
        title: '脚本发送通知',
        description: '供脚本或外部程序主动调用系统通知配置进行推送。支持普通用户 JWT，也支持带 notifications scope 的 Open API Bearer Token。未指定 channel_id / channel_ids 时走广播，只会发送到「默认推送」（push_scope=default）且已启用的渠道，设为「绑定推送」的渠道不会收到；显式指定渠道 ID 时按 ID 精确投递，绑定推送渠道同样能收到。一个「默认推送」渠道都没有时不做兜底，直接返回失败。任务自带默认通知渠道时，helper 会优先落到该渠道，传 ignore_default_config=true 可跳过它 —— 注意跳过后就是走广播，也就是只发到「默认推送」渠道，而不是发给所有渠道。面板运行脚本时会统一在脚本根目录提供 notify.py 和 sendNotify.js，不再向每个脚本子目录复制，可直接按青龙风格先收集 notifyStr 再发送。',
        auth: 'jwt',
        bodyParams: [
          { name: 'title', type: 'string', required: true, description: '通知标题', example: '签到脚本通知' },
          { name: 'content', type: 'string', required: true, description: '通知正文，通常传 notifyStr.join(\'\\n\') 或 "\\n".join(notify_lines)', example: '签到成功\\n账号: user01\\n积分: +20' },
          { name: 'channel_id', type: 'integer', description: '单个通知渠道 ID，可选；传了就必须大于 0，否则返回 400（不再静默退化成广播）', example: '3' },
          { name: 'channel_ids', type: 'array', description: '多个通知渠道 ID，可选；非空数组里必须至少有一个大于 0 的 ID，否则返回 400。空数组等同不传，按广播处理', example: '[3,5]' },
          { name: 'context', type: 'object', description: '额外模板变量，可选；供 content_template 使用', example: '{"task_name":"签到脚本","status":"success"}' },
        ],
        helperExamples: {
          JavaScript: `const { sendNotify } = require('./sendNotify')

async function main() {
  const name = '京东签到'
  const notifyStr = []
  notifyStr.push('签到成功')
  notifyStr.push('账号: user01')
  notifyStr.push('积分: +20')
  await sendNotify(name, notifyStr.join('\\n'))

  // 如需忽略任务默认通知渠道：
  // await sendNotify(name, notifyStr.join('\\n'), { channel_ids: [1, 2], ignore_default_config: true })
}

main().catch(console.error)`,
          Python: `from notify import send

def main():
    name = '京东签到'
    notify_lines = []
    notify_lines.append('签到成功')
    notify_lines.append('账号: user01')
    notify_lines.append('积分: +20')
    send(name, '\\n'.join(notify_lines))

    # 如需忽略任务默认通知渠道：
    # send(name, '\\n'.join(notify_lines), ignore_default_config=True, channel_ids=[1, 2])

if __name__ == '__main__':
    main()`,
        },
        responseExample: JSON.stringify({
          message: '通知发送完成，成功 1 个渠道',
          data: {
            sent_count: 1,
            failed_count: 0,
            channel_names: ['Telegram 主通道'],
            errors: [],
            requested_ids: [3],
            used_all: false,
            content_length: 18,
          },
        }, null, 2),
      },
    ],
  },
  {
    key: 'deps',
    label: '依赖管理',
    endpoints: [
      {
        id: 'deps-list',
        method: 'GET',
        path: '/api/deps',
        title: '获取依赖列表',
        description: '按类型获取依赖列表。响应里的 failed_by_type 是各类型安装失败数的全量汇总，不受 type / python_version 筛选影响。',
        auth: 'jwt',
        queryParams: [
          { name: 'type', type: 'string', required: true, description: '依赖类型：nodejs / python / linux', example: 'python' },
          { name: 'python_version', type: 'string', description: 'Python 版本，仅在 type=python 时生效；不传则用默认版本', example: '3.12' },
        ],
        responseExample: JSON.stringify({
          data: [{ id: 1, type: 'python', name: 'requests', status: 'installed' }],
          total: 1,
          failed_by_type: { nodejs: 2, python: 5, linux: 2 },
        }, null, 2),
        responseFields: [
          { name: 'data', type: 'array', description: '依赖列表，仅包含 type（以及 python_version）筛选命中的记录' },
          { name: 'total', type: 'integer', description: 'data 的条数' },
          { name: 'failed_by_type', type: 'object', description: '各类型安装失败的依赖数，nodejs / python / linux 三个键恒定存在（无失败时为 0）。与筛选参数无关，永远是全量统计；其中 python 跨所有 Python 版本汇总，因此三者之和等于侧栏「依赖管理」角标的数字' },
          { name: 'failed_by_type.nodejs', type: 'integer', description: 'Node.js 依赖中 status=failed 的条数' },
          { name: 'failed_by_type.python', type: 'integer', description: 'Python 依赖中 status=failed 的条数，含所有 Python 版本' },
          { name: 'failed_by_type.linux', type: 'integer', description: 'Linux 依赖中 status=failed 的条数' },
        ],
      },
      {
        id: 'deps-create',
        method: 'POST',
        path: '/api/deps',
        title: '安装依赖',
        description: '按类型批量提交依赖安装任务',
        auth: 'jwt',
        bodyParams: [
          { name: 'type', type: 'string', required: true, description: '依赖类型', example: 'python' },
          { name: 'names', type: 'array', required: true, description: '依赖名称数组', example: '["requests"]' },
        ],
        responseExample: JSON.stringify({ message: '已提交 1 个依赖安装', data: [{ id: 1, type: 'python', name: 'requests', status: 'installing' }] }, null, 2),
      },
      {
        id: 'deps-delete',
        method: 'DELETE',
        path: '/api/deps/:id',
        title: '卸载依赖',
        description: '卸载指定依赖，支持 force 强制卸载',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '依赖 ID' }],
        queryParams: [{ name: 'force', type: 'boolean', description: '是否强制卸载', example: 'true' }],
        responseExample: JSON.stringify({ message: '卸载中' }, null, 2),
      },
      {
        id: 'deps-status',
        method: 'GET',
        path: '/api/deps/:id/status',
        title: '获取依赖状态',
        description: '获取单个依赖的状态和日志',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '依赖 ID' }],
        responseExample: JSON.stringify({ data: { id: 1, type: 'python', name: 'requests', status: 'installed', log: 'install ok' } }, null, 2),
      },
      {
        id: 'deps-reinstall',
        method: 'PUT',
        path: '/api/deps/:id/reinstall',
        title: '重新安装依赖',
        description: '重新安装指定依赖',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '依赖 ID' }],
        responseExample: JSON.stringify({ message: '重新安装中' }, null, 2),
      },
    ],
  },
  {
    key: 'users',
    label: '用户管理',
    endpoints: [
      {
        id: 'users-list',
        method: 'GET',
        path: '/api/users',
        title: '获取用户列表',
        description: '获取所有用户（仅管理员）',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ id: 1, username: 'admin', role: 'admin', enabled: true, last_login_at: '2026-03-10T00:00:00' }] }, null, 2),
      },
      {
        id: 'users-create',
        method: 'POST',
        path: '/api/users',
        title: '创建用户',
        description: '创建新用户（仅管理员）',
        auth: 'jwt',
        bodyParams: [
          { name: 'username', type: 'string', required: true, description: '用户名' },
          { name: 'password', type: 'string', required: true, description: '密码（至少8位）' },
          { name: 'role', type: 'string', required: true, description: '角色：admin / operator / viewer' },
        ],
        responseExample: JSON.stringify({ message: '创建成功', data: { id: 2 } }, null, 2),
      },
      {
        id: 'users-update',
        method: 'PUT',
        path: '/api/users/:id',
        title: '更新用户',
        description: '更新用户信息（仅管理员）',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '用户 ID' }],
        bodyParams: [
          { name: 'role', type: 'string', description: '角色' },
          { name: 'enabled', type: 'boolean', description: '是否启用' },
          { name: 'password', type: 'string', description: '新密码' },
        ],
        responseExample: JSON.stringify({ message: '更新成功' }, null, 2),
      },
      {
        id: 'users-delete',
        method: 'DELETE',
        path: '/api/users/:id',
        title: '删除用户',
        description: '删除指定用户（仅管理员，不能删除自己）',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '用户 ID' }],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
    ],
  },
  {
    key: 'open',
    label: '开放 API',
    endpoints: [
      {
        id: 'open-apps-list',
        method: 'GET',
        path: '/api/open-api/apps',
        title: '获取应用列表',
        description: '获取所有开放 API 应用',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ id: 1, name: '自动化工具', client_id: 'cid_xxx', client_secret: 'cs_xxx', enabled: true, scopes: 'tasks,envs,notifications' }] }, null, 2),
      },
      {
        id: 'open-token',
        method: 'POST',
        path: '/api/open-api/token',
        title: '获取开放 API Token',
        description: '使用 App Key 和 App Secret 获取访问令牌，有效期 24 小时',
        auth: 'none',
        bodyParams: [
          { name: 'app_key', type: 'string', required: true, description: 'App Key' },
          { name: 'app_secret', type: 'string', required: true, description: 'App Secret' },
        ],
        responseExample: JSON.stringify({ data: { access_token: 'eyJhbGciOi...', token_type: 'Bearer', expires_in: 86400 } }, null, 2),
      },
    ],
  },
  {
    key: 'config',
    label: '系统配置',
    endpoints: [
      {
        id: 'config-get',
        method: 'GET',
        path: '/api/configs',
        title: '获取全部系统配置',
        description: '获取所有系统配置项及其值',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: { auto_add_cron: { key: 'auto_add_cron', value: 'true', description: '订阅拉取时自动创建任务' }, proxy_url: { key: 'proxy_url', value: '', description: '代理地址' } } }, null, 2),
      },
      {
        id: 'config-update',
        method: 'PUT',
        path: '/api/configs/batch',
        title: '批量更新配置',
        description: '批量更新系统配置（仅管理员）',
        auth: 'jwt',
        bodyParams: [
          { name: 'configs', type: 'object', required: true, description: '配置键值对', example: '{ "proxy_url": "http://127.0.0.1:7890" }' },
        ],
        responseExample: JSON.stringify({ message: '配置已保存' }, null, 2),
      },
    ],
  },
  {
    key: 'system',
    label: '系统信息',
    endpoints: [
      {
        id: 'system-info',
        method: 'GET',
        path: '/api/system/info',
        title: '获取系统信息',
        description: '获取服务器系统信息（CPU/内存/磁盘/机器码等）。自 v3.0.4 起同层追加 deployment_type（docker / binary / magisk）与 magisk_shell_version（Magisk 模块外壳版本，非模块版为 0），原有字段位置不变。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          data: { hostname: 'server', machine_code: 'A1B2C3D4-E5F6-4789-ABCD-1234567890AB', os: 'linux', arch: 'amd64', go_version: 'go1.22.0', num_cpu: 4, cpu_usage: 25.0, memory_total: 8589934592, memory_used: 4294967296, memory_usage: 50.0, disk_total: 107374182400, disk_used: 53687091200, disk_usage: 50.0, deployment_type: 'docker', magisk_shell_version: 0 },
        }, null, 2),
      },
      {
        id: 'system-machine-code',
        method: 'GET',
        path: '/api/system/machine-code',
        title: '获取面板机器码',
        description: '直接读取面板首次启动生成的机器码（UUID v4 大写格式）。不再需要手工查 SQL，OpenAPI 应用具备 system 权限即可调用。',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: { machine_code: 'A1B2C3D4-E5F6-4789-ABCD-1234567890AB' } }, null, 2),
      },
      {
        id: 'system-stats',
        method: 'GET',
        path: '/api/system/stats',
        title: '获取面板统计',
        description: '获取任务、日志、脚本的统计数据',
        auth: 'jwt',
        responseExample: JSON.stringify({
          data: { tasks: { total: 10, enabled: 8, disabled: 2, running: 1 }, logs: { total: 105, success: 90, failed: 10, aborted: 5, success_rate: 90.0 }, scripts: { total: 15 } },
        }, null, 2),
      },
    ],
  },
  {
    key: 'system-console',
    label: '系统命令行',
    endpoints: [
      {
        id: 'console-meta',
        method: 'GET',
        path: '/api/system/console/meta',
        title: '获取命令行环境信息',
        description: '获取网页版系统命令行的运行环境，供前端渲染表头与前置提示；响应是扁平结构，不套 data 层。权限：仅登录管理员（RequireAdmin），应用令牌（OpenAPI Token）不可用、一律 403 —— 这是刻意设计，因为这组接口等价于「在面板里跑任意系统命令」。边界：一次只跑一条命令（可多行），不是交互式终端，不保留 cd 与变量状态，工作目录固定为面板脚本目录（即响应里的 work_dir）。超时：单条命令的时长上限由系统配置 console_timeout_minutes 决定（默认 30 分钟，可调区间 1-720），到点终止整个进程组。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          available: true,
          unavailable_reason: '',
          shell: 'bash',
          work_dir: '/app/data/scripts',
          is_root: true,
          package_manager: 'apk',
          distribution: 'alpine',
          timeout_minutes: 30,
        }, null, 2),
        responseFields: [
          { name: 'available', type: 'boolean', description: '命令行是否可用。PATH 里找不到 bash 时为 false，此时执行接口也会直接返回 400' },
          { name: 'unavailable_reason', type: 'string', description: 'available=false 时的原因说明，可用时为空字符串' },
          { name: 'shell', type: 'string', description: '执行命令用的 Shell，固定为 bash（命令走 bash -s 的标准输入）' },
          { name: 'work_dir', type: 'string', description: '命令的工作目录，固定是面板脚本目录，与脚本管理里看到的路径一致' },
          { name: 'is_root', type: 'boolean', description: '面板进程是否以 root 运行。Windows 没有 euid 概念，一律返回 false' },
          { name: 'package_manager', type: 'string', description: '探测到的 Linux 包管理器名称（apk / apt / dnf 等），探测不到时为空字符串' },
          { name: 'distribution', type: 'string', description: '探测到的发行版标识（小写，如 alpine / debian），探测不到时为空字符串' },
          { name: 'timeout_minutes', type: 'integer', description: '当前生效的单条命令超时（分钟），取自系统配置 console_timeout_minutes' },
        ],
      },
      {
        id: 'console-run',
        method: 'POST',
        path: '/api/system/console/run',
        title: '执行命令',
        description: '提交一条命令并立即返回（201），实际输出用 run_id 轮询日志接口获取。权限：仅登录管理员（RequireAdmin），应用令牌（OpenAPI Token）不可用、一律 403，因为这等价于「在面板里跑任意系统命令」。边界：一次一条命令（可多行，会被整体喂给 bash -s），不是交互式终端 —— 没有 PTY，读标准输入的交互式命令（如未加 -y 的 apt）会直接读到 EOF 退出；每次执行都是独立进程，不保留上一条的 cd 与变量状态，工作目录固定为面板脚本目录。超时：由系统配置 console_timeout_minutes 决定（默认 30 分钟），到点终止整个进程组。后台进程：命令里用 & / nohup / setsid 起出来的孙进程会继承本次运行的输出管道。不自行重定向输出时，本次运行不会随命令本身立刻收尾，而是在命令本体退出约 2 秒后（服务端给命令行设了 WaitDelay）自行结束，按命令本体的真实退出码结算（bash 正常退出即 status=success），并在输出末尾追加一行「命令已结束；仍有后台进程持有输出管道，后续输出不再收集」，此后它的输出面板收不到了。⚠️ 注意此时本次运行已经结束：再调停止或清除记录都不会再动那个进程，它会留在容器里继续跑。只有命令仍在运行时调用停止、清除记录或撞上超时，杀的才是整个进程组。想干净收尾就自行重定向（nohup xxx >/dev/null 2>&1 &）；真要留常驻进程请改用任务/脚本，不要从这个接口起。命令为空、超过 8192 字节、或系统里没有 bash 时返回 400。',
        auth: 'jwt',
        bodyParams: [
          { name: 'command', type: 'string', required: true, description: '要执行的命令，允许多行；上限 8192 字节（按字节算，中文更容易触顶），更长的脚本请存到脚本管理里再执行', example: 'ls -al' },
        ],
        responseExample: JSON.stringify({ message: '命令已启动', run_id: 'console_1767225600000_1' }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '固定为「命令已启动」，只表示进程已拉起，不代表命令执行成功' },
          { name: 'run_id', type: 'string', description: '本次执行的运行 ID，用于轮询日志、停止与清除记录。运行记录只存在面板内存里，最多保留 50 条，面板重启即失效' },
        ],
      },
      {
        id: 'console-logs',
        method: 'GET',
        path: '/api/system/console/run/:run_id/logs',
        title: '获取命令输出',
        description: '按 run_id 轮询命令输出，响应体是 { data: { logs, discarded, done, exit_code, status } }，与脚本调试的轮询接口同形（多一个 discarded 锚点），前端可以直接复用同一套轮询逻辑。权限：仅登录管理员（RequireAdmin），应用令牌（OpenAPI Token）不可用、一律 403。边界：只读取本次执行的输出快照，不是交互式终端，无法向命令的标准输入回写内容；命令本身一次一条、不保留 cd 与变量状态，工作目录是面板脚本目录。单次运行在内存里最多保留 20 万行，超出后会成块丢弃最前面的一段并在 logs[0] 插入一行省略提示，因此增量拉取必须按 discarded 换算下标、不能拿数组长度当游标。超时：命令超过 console_timeout_minutes（默认 30 分钟）会被终止整个进程组，此时 logs 末尾会追加一行超时说明，status 记为 failed。run_id 不存在（含已被清除或被淘汰）返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'run_id', type: 'string', required: true, description: '执行命令接口返回的运行 ID' }],
        responseExample: JSON.stringify({ data: { logs: ['total 8', 'drwxr-xr-x 2 root root 4096 Jan  1 12:00 .', '[进程结束, 退出码: 0, 耗时: 0.03秒]'], discarded: 0, done: true, exit_code: 0, status: 'success' } }, null, 2),
        responseFields: [
          { name: 'data.logs', type: 'array', description: '输出行数组，标准输出与标准错误合并后按行返回；结束时会追加一行收尾说明' },
          { name: 'data.discarded', type: 'integer', description: 'logs[0] 对应的全局行号，也就是已被丢弃的行数。未触发截断时为 0。增量拉取时用「上一轮的 discarded + logs.length」当游标，本轮下标 = 游标 − discarded；算出来小于 0 说明还没渲染的行已被丢弃，应整份重新渲染' },
          { name: 'data.done', type: 'boolean', description: '是否已结束。false 时继续轮询' },
          { name: 'data.exit_code', type: 'integer', description: '退出码，运行中为 null；被手动停止时为 -1' },
          { name: 'data.status', type: 'string', description: '运行状态：running（运行中）/ success（退出码 0）/ failed（非 0 退出，含超时被杀）/ stopped（被手动停止）' },
        ],
      },
      {
        id: 'console-stop',
        method: 'PUT',
        path: '/api/system/console/run/:run_id/stop',
        title: '停止命令',
        description: '终止指定 run_id 仍在运行的命令，杀的是整个进程组（管道、后台任务一起收），随后状态记为 stopped、exit_code 记为 -1。权限：仅登录管理员（RequireAdmin），应用令牌（OpenAPI Token）不可用、一律 403。边界：命令行不是交互式终端，停止是唯一的干预手段 —— 不能向运行中的命令发送输入或信号；每条命令各自独立，不保留 cd 与变量状态，工作目录是面板脚本目录。超时：没人手动停止时，命令也会在 console_timeout_minutes（默认 30 分钟）到点后被自动终止。已结束的记录调用本接口不会报错，直接返回成功；run_id 不存在返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'run_id', type: 'string', required: true, description: '执行命令接口返回的运行 ID' }],
        responseExample: JSON.stringify({ message: '已停止' }, null, 2),
      },
      {
        id: 'console-clear',
        method: 'DELETE',
        path: '/api/system/console/run/:run_id',
        title: '清除运行记录',
        description: '删除指定 run_id 的运行记录（含已缓存的输出）；记录里的进程如果还在跑会一并杀掉，避免记录没了、进程还占着资源却再也找不到停止入口。权限：仅登录管理员（RequireAdmin），应用令牌（OpenAPI Token）不可用、一律 403。边界：运行记录只存在面板内存里，最多保留 50 条、面板重启即清空，本接口只做提前回收；命令行本身一次一条命令、不是交互式终端，不保留 cd 与变量状态，工作目录是面板脚本目录。超时：未清除的命令仍受 console_timeout_minutes（默认 30 分钟）约束，到点终止整个进程组。run_id 不存在也返回成功（幂等），清除后再查日志会得到 404。',
        auth: 'jwt',
        pathParams: [{ name: 'run_id', type: 'string', required: true, description: '执行命令接口返回的运行 ID' }],
        responseExample: JSON.stringify({ message: '已清除' }, null, 2),
      },
    ],
  },
  {
    key: 'backup',
    label: '系统备份',
    endpoints: [
      {
        id: 'backup-create',
        method: 'POST',
        path: '/api/system/backup',
        title: '一键创建备份',
        description: '创建数据库与脚本数据备份；支持管理员账号 Token 或 OpenAPI 应用（需 backup 权限）调用。',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: false, example: 'auto-backup', description: '备份文件名前缀，可省略由系统自动生成' },
          { name: 'password', type: 'string', required: false, example: '', description: '可选加密密码，留空则导出明文 .json 文件' },
          { name: 'selection', type: 'object', required: false, example: '{}', description: '可选模块选择，留空导出全部内容' },
        ],
        responseExample: JSON.stringify({ data: { path: 'data/backups/daidai-backup-20260101-120000.json' }, message: '备份成功' }, null, 2),
      },
      {
        id: 'backup-list',
        method: 'GET',
        path: '/api/system/backups',
        title: '获取备份列表',
        description: '列出当前面板已生成的全部备份文件',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ filename: 'daidai-backup-20260101.json', size: 102400, modified_at: '2026-01-01T12:00:00Z' }] }, null, 2),
      },
      {
        id: 'backup-delete',
        method: 'DELETE',
        path: '/api/system/backup',
        title: '删除备份文件',
        description: '按文件名删除一个备份文件（filename 通过 query 传递）',
        auth: 'jwt',
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
    ],
  },
]

export function generateCodeExamples(endpoint: ApiEndpoint): Record<string, string> {
  const { method, path, bodyParams, auth } = endpoint
  const url = `${getApiBaseOrigin()}${path}`
  const hasBody = bodyParams && bodyParams.length > 0 && method !== 'GET'

  // 票据鉴权的下载接口不能套用通用模板：它没有 Authorization 头，响应也不是 JSON，
  // 而且必须先调同路径的 -ticket 接口换票，示例要把这两步一起给出来。
  if (auth === 'ticket') {
    return generateTicketDownloadExamples(url)
  }

  const bodyObj: Record<string, any> = {}
  if (hasBody) {
    bodyParams!.forEach(p => {
      if (p.example) {
        if (p.type === 'integer') {
          bodyObj[p.name] = Number(p.example)
        } else if (p.type === 'boolean') {
          bodyObj[p.name] = p.example === 'true'
        } else if (p.type === 'array' || p.type === 'object') {
          try {
            bodyObj[p.name] = JSON.parse(p.example)
          } catch {
            bodyObj[p.name] = p.example
          }
        } else {
          bodyObj[p.name] = p.example
        }
      } else {
        bodyObj[p.name] = p.type === 'integer' ? 0 : p.type === 'boolean' ? false : p.type === 'array' ? [] : p.type === 'object' ? {} : ''
      }
    })
  }
  const bodyJson = JSON.stringify(bodyObj, null, 2)

  const authHeader = auth === 'jwt' ? `-H "Authorization: Bearer <TOKEN>"` : ''

  let shell = `curl -X ${method} "${url}"`
  if (authHeader) shell += ` \\\n  ${authHeader}`
  if (hasBody) shell += ` \\\n  -H "Content-Type: application/json" \\\n  -d '${bodyJson}'`

  let js = `const res = await fetch("${url}", {\n  method: "${method}",\n  headers: {\n    "Content-Type": "application/json",`
  if (auth === 'jwt') js += `\n    "Authorization": "Bearer <TOKEN>",`
  js += `\n  },`
  if (hasBody) js += `\n  body: JSON.stringify(${bodyJson}),`
  js += `\n})\nconst data = await res.json()\nconsole.log(data)`

  let python = `import requests\n\n`
  if (auth === 'jwt') python += `headers = {"Authorization": "Bearer <TOKEN>"}\n`
  if (hasBody) {
    python += `data = ${bodyJson}\n`
    python += `res = requests.${method.toLowerCase()}("${url}"${auth === 'jwt' ? ', headers=headers' : ''}, json=data)\n`
  } else {
    python += `res = requests.${method.toLowerCase()}("${url}"${auth === 'jwt' ? ', headers=headers' : ''})\n`
  }
  python += `print(res.json())`

  return { Shell: shell, JavaScript: js, Python: python }
}

function generateTicketDownloadExamples(downloadUrl: string): Record<string, string> {
  const origin = getApiBaseOrigin()
  const ticketUrl = `${downloadUrl}-ticket`

  const shell = `# 1. 换票：这一步走正常 Bearer 鉴权
curl -X GET "${ticketUrl}" \\
  -H "Authorization: Bearer <TOKEN>"
# → {"url":"...?ticket=...","filename":"...","size":20480,"expires_in":120}

# 2. 直传下载：票据在 URL 上，这一步不要再带 Authorization
#    url 是相对路径，拼到面板地址后面即可；-OJ 按 Content-Disposition 存盘
curl -OJ "${origin}<上一步返回的 url>"`

  const js = `// 1. 换票：这一步走正常 Bearer 鉴权
const res = await fetch("${ticketUrl}", {
  headers: {
    "Authorization": "Bearer <TOKEN>",
  },
})
const ticket = await res.json()

// 2. 直传下载：交给浏览器原生下载，文件不进 JS 内存，也带不上 Authorization 头
const anchor = document.createElement("a")
anchor.href = ticket.url
anchor.download = ticket.filename
anchor.click()`

  const python = `import requests

BASE = "${origin}"

# 1. 换票：这一步走正常 Bearer 鉴权
ticket = requests.get("${ticketUrl}", headers={"Authorization": "Bearer <TOKEN>"}).json()

# 2. 直传下载：票据在 URL 上，这一步不要再带 Authorization；响应是文件流，不是 JSON
with requests.get(BASE + ticket["url"], stream=True) as res:
    res.raise_for_status()
    with open(ticket["filename"], "wb") as f:
        for chunk in res.iter_content(65536):
            f.write(chunk)`

  return { Shell: shell, JavaScript: js, Python: python }
}

export function getApiBaseOrigin(): string {
  if (typeof window !== 'undefined' && window.location?.origin) {
    return window.location.origin
  }
  return 'http://localhost:5701'
}
