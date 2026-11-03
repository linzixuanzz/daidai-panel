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
  /** 生成「请求示例代码」时额外带上的请求头（例如 MCP 接口必须带的 Accept）；鉴权头不写在这里 */
  extraHeaders?: Record<string, string>
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
        description: '插件对接的第一步：使用在面板「Open API」页面创建的应用 App Key 和 App Secret 获取 access_token。Token 有效期 24 小时，过期后需重新调用此接口获取新 Token（无 refresh_token 机制）。同一 IP 10 分钟内凭证错误超过 10 次会被暂时拒绝（429），凭证正确的请求不计次数。',
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
        id: 'auth-passkey-login-begin',
        method: 'POST',
        path: '/api/auth/passkey/login/begin',
        title: '通行密钥登录：获取挑战',
        description: '通行密钥（Passkey / WebAuthn）登录的第一步，免登录。返回一次性的 session_id 和浏览器 navigator.credentials.get 要用的 publicKey 请求选项（二进制字段都是 base64url 无填充，调用前需解码成 ArrayBuffer）。用的是可发现凭据，allowCredentials 为空，不需要先输用户名。⚠️ 浏览器只在安全上下文里提供 WebAuthn：必须用 HTTPS 域名（或 http://localhost）访问面板，用 http://IP:端口 访问时不可用。RP ID 取自请求头 Origin 的主机名，必须是域名、不能是 IP；每个通行密钥只绑定添加时的完整访问地址（协议 + 主机名 + 端口，如 https://panel.example.com、http://localhost:5792），换地址或端口访问要重新添加。挑战 5 分钟内有效、只能用一次。本接口与第二步单独限流（每分钟 20 次），挑战总量也有上限，被刷满时回 429。通行密钥是额外的登录方式，不改变密码登录和两步验证的任何行为；APP 不支持，继续用密码登录。',
        auth: 'none',
        responseExample: JSON.stringify({
          '成功 (200)': {
            data: {
              session_id: 'pQ0b3H6m...',
              options: {
                challenge: 'Vf2c1Yk9...',
                timeout: 300000,
                rpId: 'panel.example.com',
                userVerification: 'required',
              },
            },
          },
          '访问地址不支持 (400)': {
            error: '当前访问地址不支持通行密钥，请通过 HTTPS 域名访问面板',
            code: 'passkey_unsupported_origin',
          },
          '请求过多 (429)': { error: '请求过于频繁，请稍后再试' },
        }, null, 2),
        responseFields: [
          { name: 'data.session_id', type: 'string', description: '本次挑战的会话 ID，第二步原样带回；一次性，5 分钟过期' },
          { name: 'data.options', type: 'object', description: 'publicKey 请求选项（challenge / timeout / rpId / userVerification），challenge 是 base64url' },
          { name: 'code', type: 'string', description: '失败时的机器可读标识：passkey_unsupported_origin = Origin 缺失、不是 https、或主机名是 IP（http 只放行 localhost）' },
        ],
      },
      {
        id: 'auth-passkey-login-finish',
        method: 'POST',
        path: '/api/auth/passkey/login/finish',
        title: '通行密钥登录：提交验证结果',
        description: '通行密钥登录的第二步，免登录。把 navigator.credentials.get 返回的凭据（二进制字段编码成 base64url）连同 session_id 交回。成功时响应体与 POST /api/auth/login 成功时逐字段相同，同样建立登录会话、记登录日志、按设置发登录通知。与密码登录一样受 IP 白名单约束；不需要人机验证，也不受密码登录失败锁定的影响（锁定是防密码爆破的，被别人试密码锁住时本人仍可用通行密钥登录），也不需要再输两步验证码。session_id 无论成败都只能用一次，失败后要从第一步重来。',
        auth: 'none',
        bodyParams: [
          { name: 'session_id', type: 'string', required: true, description: '第一步返回的 session_id' },
          { name: 'credential', type: 'object', required: true, description: 'PublicKeyCredential 的 JSON：id、rawId、type、response.clientDataJSON / authenticatorData / signature / userHandle（二进制字段都是 base64url）' },
        ],
        responseExample: JSON.stringify({
          '登录成功 (200)': {
            access_token: 'eyJhbGciOi...',
            refresh_token: 'eyJhbGciOi...',
            user: { id: 1, username: 'admin', role: 'admin' },
          },
          '挑战已过期 (400)': {
            error: '通行密钥验证已过期，请重试',
            code: 'passkey_session_expired',
          },
          '验证失败 (401)': {
            error: '通行密钥验证失败，请重试或改用密码登录',
            code: 'invalid_passkey',
          },
          '账号已禁用 (403)': { error: '账号已被禁用' },
        }, null, 2),
        responseFields: [
          { name: 'access_token / refresh_token / user', type: 'object', description: '与 POST /api/auth/login 成功时完全相同' },
          { name: 'code', type: 'string', description: '失败时的机器可读标识：passkey_session_expired = session_id 不存在、已用过或超过 5 分钟；invalid_passkey = 找不到这个通行密钥、访问地址（域名 / 端口）与添加时不符、用户句柄不符、签名或用户验证不通过' },
          { name: '403', type: 'tip', description: 'IP 不在白名单、或账号被禁用时返回 403，文案与密码登录一致' },
        ],
      },
      {
        id: 'auth-refresh',
        method: 'POST',
        path: '/api/auth/refresh',
        title: '刷新令牌',
        description: '使用 refresh_token 获取新的 access_token，请求头带 Authorization: Bearer <refresh_token>。refresh_token 不轮换：响应里只有新的 access_token，之后继续用原来那枚 refresh_token 续期。续期得来的 access_token 与登录时那枚属于同一个登录会话，同一会话可以同时存在多枚有效的 access_token（多个标签页各自续期）。所属登录会话已退出或被撤销时（退出登录、在会话管理里被撤销、修改或重置密码、禁用或删除用户、修改角色或用户名、被同类型的新登录顶替）返回 401 {"error":"令牌无效或已过期"}，需要重新登录。本接口有限流：同一 IP 每分钟超过 30 次返回 429。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          '续期成功 (200)': { access_token: 'eyJhbGciOi...' },
          '登录会话已失效 (401)': { error: '令牌无效或已过期' },
        }, null, 2),
        responseFields: [
          { name: 'access_token', type: 'string', description: '新的访问令牌，签发后立即可用' },
          { name: '已鉴权接口的 401', type: 'tip', description: '登录令牌所属的会话失效后，任何已鉴权接口都返回 401 {"error":"登录已失效，请重新登录","code":"session_revoked"}。收到后先调本接口续期一次：会话还在（例如面板升级前续期得来的旧令牌）就能拿到新的 access_token 接着用；本接口也返回 401 时才需要重新登录。脚本令牌与开放 API 应用令牌被作废时仍返回 {"error":"令牌已被撤销"}，不带 code' },
        ],
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
        description: '修改当前用户密码。成功后该用户所有登录（包括当前这个）都会被撤销、需要重新登录，并会一并清除该用户的通行密钥。',
        auth: 'jwt',
        bodyParams: [
          { name: 'old_password', type: 'string', required: true, description: '当前密码' },
          { name: 'new_password', type: 'string', required: true, description: '新密码：8-128 位，且字母、数字、符号至少包含两种' },
        ],
        responseExample: JSON.stringify({ message: '密码修改成功' }, null, 2),
      },
      {
        id: 'auth-preferences-get',
        method: 'GET',
        path: '/api/auth/preferences',
        title: '获取当前用户的界面偏好',
        description: '读取当前登录用户自己的界面偏好，分两组：editor 是编辑器（脚本编辑 / 日志查看）的开关；list 是列表页 / 日志查看等界面偏好（定时任务页、环境变量页的每页条数，定时任务页视图栏「全部」与「顶栏分组页签」的显隐，以及 v3.3.3 起的「打开已结束的日志时是否定位到底部」，这一项面板网页与 APP 共用）。⚠️ 这是【按用户】的偏好，不是全面板配置：每个账号各存一份、互不影响，也不在 /api/configs 里，换浏览器、换域名 / IP 登录都跟着账号走。鉴权只要求 JWTAuth、不限角色——operator / viewer 同样能读写自己那份，改自己的界面开关不该要管理员权限（对比 /api/configs 那组是管理员独占）。两组的存储口径不同：editor 从没存过时直接返回一整套默认值，不会 404，但会带上 stored=false（见响应字段，这是升级路径的关键）；list 是稀疏存储，只下发用户存过、且取值合法的键，从没存过就是 {}，服务端不替它补默认值。⚠️ 编辑器引擎（CodeMirror / Monaco）刻意【不在】这里：它带着设备自适应语义（触摸设备与窄屏硬回落 CodeMirror），只存在浏览器本地——引擎跟设备走，偏好跟人走。',
        auth: 'jwt',
        // 示例刻意取「只存过 list、没存过 editor」的用户：stored=false 与 list 非空同时出现，
        // 正好说明 stored 只管 editor 那一组，读文档的人不会把它当成「整份偏好存没存过」。
        responseExample: JSON.stringify({
          editor: {
            word_wrap: 'on',
            minimap: false,
            indent_guides: true,
            whitespace: 'selection',
            indent_width: 'auto',
          },
          stored: false,
          list: {
            tasks_page_size: 50,
            tasks_view_all_hidden: true,
          },
        }, null, 2),
        responseFields: [
          { name: 'editor.word_wrap', type: 'string', description: '自动换行，取值 on / off，默认 on' },
          { name: 'editor.minimap', type: 'boolean', description: '缩略图开关，JSON 布尔，默认 false。只对 Monaco 引擎生效（CodeMirror 没有缩略图）' },
          { name: 'editor.indent_guides', type: 'boolean', description: '缩进参考线开关，JSON 布尔，默认 true' },
          { name: 'editor.whitespace', type: 'string', description: '空白符显示，取值 none（从不）/ selection（仅选中）/ all（始终），默认 selection' },
          { name: 'editor.indent_width', type: 'string', description: '缩进宽度，取值 auto / 2 / 4 / 6 / 8，默认 auto（按文件内容自动检测）。⚠️ 传输层是字符串，是 "4" 不是 4' },
          { name: 'stored', type: 'boolean', description: '服务端是否真的存过这个用户的编辑器偏好（只看 editor 这一组）。判定口径：有这个用户的记录 且 记录里的编辑器偏好非空、能解析成 JSON 对象 → true；没有记录 / 空串 / 脏 JSON 一律 false（后两种当成「没存过」）。⚠️ 它是给客户端做升级路径判断用的：stored=false 说明服务端还没存过这个用户的编辑器偏好，此时客户端应当把【本机】那份偏好一次性 PUT 上去做上行迁移，而不是拿下面 editor 里这套默认值去覆盖本机——照着覆盖的话，老用户升级到有服务端偏好的版本时，本机调好的开关会在首屏被静默冲掉。stored=true 才允许拿 editor 的值写回本地。注意 editor 字段本身不受 stored 影响：无论 stored 是什么都下发一整套可用的值，不认得 stored 的老客户端行为与加这个字段之前逐字一致。⚠️ stored 只描述 editor 这一组，与 list 无关：只存过列表页偏好、没碰过编辑器开关的用户，stored 仍是 false' },
          { name: 'list', type: 'object', description: '列表页 / 日志查看等界面偏好，稀疏存储：只含用户存过、且取值合法的键，从没存过就是 {}。list 没有 stored 标记——某个键在不在就说明它存没存过；缺的键客户端按下面各项写的默认值处理，服务端不下发默认值。库里存了脏值的键会被丢弃、不下发，不影响其余键。老版本服务端（v3.3.1 之前）的响应里没有这个字段' },
          { name: 'list.tasks_page_size', type: 'number', description: '定时任务页每页条数，JSON 数字，取值 10 / 20 / 50 / 100。未设置时不下发，客户端按 20 处理。手机与电脑共用这一个值' },
          { name: 'list.envs_page_size', type: 'string', description: '环境变量页每页条数，取值 "20" / "50" / "100" / "all"（all 表示一页显示全部）。⚠️ 传输层是字符串（因为有 all），是 "50" 不是 50。未设置时不下发，客户端按 "20" 处理' },
          { name: 'list.tasks_view_all_hidden', type: 'boolean', description: '定时任务页视图栏是否隐藏内置的「全部」标签，JSON 布尔。未设置时不下发，客户端按 false（显示）处理。自定义视图全部隐藏时，标签栏仍会保底显示「全部」' },
          { name: 'list.tasks_view_groups_hidden', type: 'boolean', description: '定时任务页视图栏是否整体隐藏顶栏分组页签（每个分组一个页签，来自任务的「分组:」标签；网页「视图管理」里叫「顶栏分组页签」的那个开关），JSON 布尔。未设置时不下发，客户端按 false（显示）处理。⚠️ 它不管列表里任务名旁边的分组小标签（那是网页「显示设置」里的「分组标签」，只存在本浏览器）；各个自定义视图自己的显隐也不在这里，那是全面板共用的，走视图管理接口' },
          { name: 'list.log_open_at_bottom', type: 'boolean', description: '打开【已结束】的日志时是否直接定位到底部，JSON 布尔，面板网页与 APP 共用。未设置时不下发，各客户端按自己的现状处理：面板网页按 false（停在开头），APP 按 true（底部）。运行中的日志一律自动跟随最新输出，不看这一项。v3.3.3 起才有：v3.3.1 / v3.3.2 的服务端会把它当成白名单外的键丢掉，PUT 回 200 但响应的 list 里没有它' },
        ],
      },
      {
        id: 'auth-preferences-update',
        method: 'PUT',
        path: '/api/auth/preferences',
        title: '更新当前用户的界面偏好',
        description: '更新当前登录用户自己的界面偏好。body 分 editor、list 两组，两组都可选。⚠️ 两组各自做字段级合并：body 里只放要改的键，没传的键保持原值、不会被重置成默认值——面板自己就是这么用的（改一个开关只提交那一个键）。服务端把「读出当前值 → 合并 → 写回」整段串行执行，所以几个 PUT 同时到达（两个标签页各改各的开关、一次保存连发两个请求）时，各自改的不同键都会落库、不会互相覆盖；同一个键被同时改时以后执行的那次为准。只带其中一组时，另一组原样不动：只改列表页每页条数不会顺手写入编辑器偏好，也不会把 stored 翻成 true。两组都不带（比如发 {}）是 no-op，回 200 并原样返回当前值（v3.3.1 之前发 {} 会写入一整套编辑器默认值并把 stored 置为 true，现已改掉）。鉴权同 GET：JWTAuth、不限角色，改的永远是调用者自己那份，没有「替别人改」的入口。任一字段取值或类型非法返回 400，这次请求两组都不会写入，不做静默纠正。响应体是合并之后的当前值，与 GET 同形。',
        auth: 'jwt',
        bodyParams: [
          { name: 'editor', type: 'object', required: false, description: '编辑器偏好，取下面 5 个键的任意子集。不带这一项时编辑器偏好保持原样', example: '{ "whitespace": "all" }' },
          { name: 'editor.word_wrap', type: 'string', description: '自动换行，取值 on / off', example: 'on' },
          { name: 'editor.minimap', type: 'boolean', description: '缩略图开关（只对 Monaco 引擎生效）。写入时是 JSON 布尔——面板前端提交的就是布尔；另外也接受 "on" / "off" / "true" / "false" 字符串，那是留给 APP 与历史客户端的。读出来统一是 JSON 布尔', example: 'false' },
          { name: 'editor.indent_guides', type: 'boolean', description: '缩进参考线开关。写入时是 JSON 布尔（面板前端提交的就是布尔），同样兼容 "on" / "off" / "true" / "false" 字符串以照顾 APP 与历史客户端。读出来统一是 JSON 布尔', example: 'true' },
          { name: 'editor.whitespace', type: 'string', description: '空白符显示，字符串，取值 none / selection / all', example: 'all' },
          { name: 'editor.indent_width', type: 'string', description: '缩进宽度，字符串，取值 auto / 2 / 4 / 6 / 8。⚠️ 传输层是字符串，要传 "4" 不是 4', example: 'auto' },
          { name: 'list', type: 'object', required: false, description: '列表页 / 日志查看等界面偏好，取下面 5 个键的任意子集，只存你传了的键（稀疏存储）。不带这一项时这一组保持原样。⚠️ 每个键的 JSON 类型是固定的，类型不对（比如把 tasks_page_size 传成 "50"）同样回 400', example: '{ "tasks_page_size": 50 }' },
          { name: 'list.tasks_page_size', type: 'number', description: '定时任务页每页条数，JSON 数字，取值 10 / 20 / 50 / 100', example: '50' },
          { name: 'list.envs_page_size', type: 'string', description: '环境变量页每页条数，字符串，取值 "20" / "50" / "100" / "all"。⚠️ 传输层是字符串（因为有 all），要传 "50" 不是 50', example: 'all' },
          { name: 'list.tasks_view_all_hidden', type: 'boolean', description: '定时任务页视图栏是否隐藏「全部」标签，只收 JSON 布尔（这是新接口，不兼容 "on" / "off" 字符串）', example: 'true' },
          { name: 'list.tasks_view_groups_hidden', type: 'boolean', description: '定时任务页视图栏是否整体隐藏顶栏分组页签（网页「视图管理」里的「顶栏分组页签」开关），只收 JSON 布尔', example: 'false' },
          { name: 'list.log_open_at_bottom', type: 'boolean', description: '打开已结束的日志时是否直接定位到底部，只收 JSON 布尔（传 "on" 或 1 回 400）。面板网页与 APP 共用这一个值', example: 'true' },
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
          list: {
            tasks_page_size: 50,
          },
        }, null, 2),
        responseFields: [
          { name: 'editor', type: 'object', description: '合并之后的完整编辑器偏好，字段说明见「获取当前用户的界面偏好」' },
          { name: 'stored', type: 'boolean', description: '与 GET 的 stored 同一口径，只描述 editor 这一组，按实际情况判定：本次写入了 editor、或者此前就存过编辑器偏好时为 true；这次只写了 list（或发的是 {}）、而编辑器偏好从没存过时仍为 false。⚠️ 它不是「这次 PUT 成功了」的标志，成功与否看 HTTP 状态码；客户端照旧据它判断要不要把本机的编辑器偏好上行迁移' },
          { name: 'list', type: 'object', description: '合并之后的 list 组偏好，仍是稀疏的：只含存过的键，字段说明见「获取当前用户的界面偏好」' },
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
      {
        id: 'auth-passkeys-list',
        method: 'GET',
        path: '/api/security/passkeys',
        title: '获取我的通行密钥',
        description: '列出当前登录用户自己添加的通行密钥，按添加时间倒序。任何角色都能调用，只能看到自己的。公钥、用户句柄、凭据原文一律不下发。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          data: [
            {
              id: 3,
              name: 'Windows · Chrome',
              rp_id: 'panel.example.com',
              origin: 'https://panel.example.com',
              created_at: '2026-11-03T10:00:00+08:00',
              last_used_at: null,
              backup_state: true,
            },
          ],
        }, null, 2),
        responseFields: [
          { name: 'data[].id', type: 'integer', description: '通行密钥 ID，重命名 / 删除时用' },
          { name: 'data[].name', type: 'string', description: '名称（最多 64 个字）' },
          { name: 'data[].rp_id', type: 'string', description: '绑定的域名（WebAuthn 的 RP ID，不含端口）' },
          { name: 'data[].origin', type: 'string', description: '添加时的完整访问地址（协议 + 主机名 + 端口），如 https://panel.example.com、http://localhost:5792。只能在这个地址下用它登录，换地址或端口访问面板要重新添加' },
          { name: 'data[].created_at', type: 'string', description: '添加时间' },
          { name: 'data[].last_used_at', type: 'string', description: '最近一次用它登录的时间，从没用过为 null' },
          { name: 'data[].backup_state', type: 'boolean', description: '认证器报告的备份状态：true 表示这个通行密钥会随系统账号 / 密码管理器同步到其它设备' },
        ],
      },
      {
        id: 'auth-passkeys-register-begin',
        method: 'POST',
        path: '/api/security/passkeys/register/begin',
        title: '添加通行密钥：获取注册选项',
        description: '为当前登录用户添加通行密钥的第一步。必须带当前密码——防止会话被劫持后被人偷偷加一个通行密钥长期驻留。返回 session_id 和 navigator.credentials.create 要用的 publicKey 创建选项（二进制字段 base64url）：要求可发现凭据（residentKey=required）与用户验证（userVerification=required），attestation=none，excludeCredentials 是该用户在本域名下已有的通行密钥。与登录一样必须通过 HTTPS 域名（或 http://localhost）访问，RP ID 取自请求头 Origin，不能是 IP；添加时的完整访问地址（含端口）会记下来，以后只能在这个地址下用它登录。当前用户开启了两步验证时还必须带 totp_code。每个账号最多 20 个。',
        auth: 'jwt',
        bodyParams: [
          { name: 'password', type: 'string', required: true, description: '当前登录用户的密码' },
          { name: 'totp_code', type: 'string', required: false, description: '两步验证码（6 位数字）。当前用户开启了两步验证时必填，没开时不用传', example: '123456' },
        ],
        responseExample: JSON.stringify({
          '成功 (200)': {
            data: {
              session_id: 'Jx8nq2...',
              options: {
                rp: { name: '呆呆面板', id: 'panel.example.com' },
                user: { id: 'k3Vb...', name: 'admin', displayName: 'admin' },
                challenge: 'c2Vj...',
                pubKeyCredParams: [{ type: 'public-key', alg: -7 }, { type: 'public-key', alg: -257 }],
                timeout: 300000,
                excludeCredentials: [],
                authenticatorSelection: { residentKey: 'required', requireResidentKey: true, userVerification: 'required' },
                attestation: 'none',
              },
            },
          },
          '密码错误 (400)': { error: '密码错误' },
          '缺少两步验证码 (400)': { error: '请输入两步验证码', code: 'two_factor_required' },
          '两步验证码错误 (400)': { error: '两步验证码错误', code: 'invalid_totp' },
          '数量已满 (400)': { error: '每个账号最多添加 20 个通行密钥' },
          '访问地址不支持 (400)': {
            error: '当前访问地址不支持通行密钥，请通过 HTTPS 域名访问面板',
            code: 'passkey_unsupported_origin',
          },
        }, null, 2),
        responseFields: [
          { name: 'data.session_id', type: 'string', description: '第二步原样带回；一次性，5 分钟过期，只能由发起的这个用户使用' },
          { name: 'data.options', type: 'object', description: 'publicKey 创建选项；challenge、user.id、excludeCredentials[].id 是 base64url，调用前需解码' },
          { name: 'code', type: 'string', description: '失败时的机器可读标识：two_factor_required = 已开启两步验证但没带 totp_code；invalid_totp = 两步验证码错误；passkey_unsupported_origin = 访问地址不是 HTTPS 域名 / http://localhost' },
        ],
      },
      {
        id: 'auth-passkeys-register-finish',
        method: 'POST',
        path: '/api/security/passkeys/register/finish',
        title: '添加通行密钥：提交注册结果',
        description: '添加通行密钥的第二步：把 navigator.credentials.create 的结果（二进制字段 base64url）连同 session_id 交回保存。name 去掉首尾空白后截到 64 个字，留空时用「通行密钥」。session_id 过期或已用过时回 400，code=passkey_session_expired。',
        auth: 'jwt',
        bodyParams: [
          { name: 'session_id', type: 'string', required: true, description: '第一步返回的 session_id' },
          { name: 'name', type: 'string', required: false, description: '名称，方便以后认出是哪台设备', example: 'Windows · Chrome' },
          { name: 'credential', type: 'object', required: true, description: 'PublicKeyCredential 的 JSON：id、rawId、type、response.clientDataJSON / attestationObject，可选 response.transports' },
        ],
        responseExample: JSON.stringify({
          '成功 (200)': {
            message: '通行密钥已添加',
            data: {
              id: 3,
              name: 'Windows · Chrome',
              rp_id: 'panel.example.com',
              origin: 'https://panel.example.com',
              created_at: '2026-11-03T10:00:00+08:00',
              last_used_at: null,
              backup_state: true,
            },
          },
          '重复添加 (400)': { error: '这个通行密钥已经添加过了' },
          '挑战已过期 (400)': {
            error: '通行密钥验证已过期，请重试',
            code: 'passkey_session_expired',
          },
        }, null, 2),
        responseFields: [
          { name: 'data', type: 'object', description: '新添加的这一条，字段同「获取我的通行密钥」' },
        ],
      },
      {
        id: 'auth-passkeys-rename',
        method: 'PUT',
        path: '/api/security/passkeys/:id',
        title: '重命名通行密钥',
        description: '修改自己的某个通行密钥的名称。不是自己的（或不存在）返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '通行密钥 ID' }],
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '新名称，最多 64 个字', example: 'iPhone · Safari' },
        ],
        responseExample: JSON.stringify({ message: '已重命名' }, null, 2),
      },
      {
        id: 'auth-passkeys-delete',
        method: 'DELETE',
        path: '/api/security/passkeys/:id',
        title: '删除通行密钥',
        description: '删除自己的某个通行密钥，删除后不能再用它登录。不是自己的（或不存在）返回 404。只删面板这边的记录，设备或密码管理器里保存的那一份需要用户自己去系统里删。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '通行密钥 ID' }],
        responseExample: JSON.stringify({ message: '已删除' }, null, 2),
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
          { name: 'label', type: 'string', description: '按标签模糊匹配（labels LIKE %值%）。分组要连前缀一起传，例如「分组:日常」，只传「日常」会把普通标签也捞进来；只想按分组精确筛选请用 filters 的 group 字段' },
          { name: 'filters', type: 'string', description: '任务视图的筛选规则，JSON 字符串数组 [{field,operator,value}]。field 可选 command / name / cron_expression / status / labels / subscription / group；operator 可选 contains / not_contains / equals / not_equals；匹配一律先 trim 再转小写、不是正则；多条之间是 AND。status 的 value 用 1（已启用）/ 0（已禁用）/ 2（运行中）/ 0.5（排队中）。group 只认任务的分组名（labels 里 trim 后第一个名字非空的「分组:xxx」），value 填名字本身、不带「分组:」前缀，不会误中同名的普通标签或订阅名；没有分组的任务，equals / contains 不命中，not_equals / not_contains 命中；现有分组见 GET /api/tasks/groups。value 为空的规则会被整条丢弃，JSON 解析失败则静默降级为不筛选', example: '[{"field":"command","operator":"contains","value":"jd"}]' },
          { name: 'sort_rules', type: 'string', description: '任务视图的排序规则，JSON 字符串数组 [{field,direction}]。field 可选 name / command / cron_expression / status / labels / subscription / group / created_at / last_run_at / next_run_at（group 按分组名比较、不区分大小写，没有分组的按空串参与比较）；direction 只认 desc，其它一律按 asc。⚠️ 置顶与状态分组【压在排序规则之上】，排序规则只在同一分区内生效：先按置顶分区（置顶的整体在前），再按状态分区（启用/排队中/运行中一组、禁用一组、其余一组），所以「按最后运行倒序」不会把置顶任务冲散、也不会把禁用任务混进启用任务中间。⚠️ 状态分区有一条【豁免】：当 sort_rules 的【第一条】规则 field = "status" 时，状态分区不生效，整份列表按 status 值原样排序（置顶分区仍然生效，不受影响）。理由是状态分区本身就是按 status 分的，再按 status 排一次等于自相矛盾——用户显式点了「按状态排序」，拿到的却是被状态分区钉死的固定组序。只看第一条是因为它才代表用户的主排序意图，第二条起是 tie-break。分区内多条规则按先后做 tie-break，全平手再回落默认排序：运行中优先 > 拖拽顺序（list_order）> 手工顺序（sort_order）> 创建时间。⚠️「运行中优先」与上面的置顶 / 状态分区【不是同一层】，别混为一谈：置顶与状态分区压在【所有】排序规则之上，而运行中优先只是默认排序链里的一环 —— 不传 sort_rules 时它把 status=2（运行中）的任务提到【本区】最前，传了 sort_rules 时只有规则全部打平才轮得到它。所以用户显式点了「名称 A→Z」，运行中不会越过他的规则插到前面。这一层只认 2（运行中），不认 0.5（排队中）——排队是几秒钟就过去的瞬时态，一并提上来只会让同一次运行多跳一次；任务跑完自动回到原位，list_order 不会被改写（拖拽顺序不受影响，PUT /api/tasks/sort 的「区」划分也不受影响）。cron_expression（定时规则）、last_run_at（最后运行）与 next_run_at（下次运行）是任务列表页三列的点击排序：next_run_at 不是数据库列，是按 cron 现算的快照，只能在内存里排；last_run_at / next_run_at 的空值（从未运行、已禁用或非 cron 因而没有下次运行）在各自分区内一律排最后，不随 asc/desc 翻转。cron_expression 从 v3.3.3 起不再按表达式字符串比较，而是按「每天最早一次执行的时刻」（时:分:秒）比较：每行 cron 从当天 0 点起取第一次触发的时刻，多行取最早；与「现在几点」无关，刷新不会变序；周一 09:00、每天 09:00、5 段与 6 段写法的 09:00 都按 09:00 排在一起；高频任务（如 */5、每 10 秒）按 00:00 计，带时段的（如 9-22 点每 10 分钟）按时段起点计；不看启用状态，禁用任务在禁用分区内同样按时刻排；空值（手动运行 / 开机运行、表达式为空、永不触发如 2 月 30 日）在各自分区内一律排最后，同样不随 asc/desc 翻转；时刻相同时交给下一条规则。未知 field 静默回落默认排序，不报 400', example: '[{"field":"next_run_at","direction":"asc"}]' },
        ],
        responseExample: JSON.stringify({
          data: [{
            id: 1,
            name: '签到任务',
            command: 'task sign.py',
            cron_expression: '0 9 * * *',
            status: 1,
            enabled: true,
            last_run_status: 0,
            last_run_at: '2026-03-10T09:00:00',
            notify_on_abort: false,
            notification_channel_id: 3,
            notification_channel_name: 'Telegram 主通道',
          }],
          total: 1, page: 1, page_size: 20,
        }, null, 2),
        responseFields: [
          { name: 'data[].status', type: 'number', description: '运行状态：1 空闲（已启用）/ 0 已禁用 / 0.5 排队中 / 2 运行中。禁用的任务被手动运行期间也是 0.5 或 2，所以不能拿它判断启用开关' },
          { name: 'data[].enabled', type: 'boolean', description: '启用开关，与是否正在运行无关：禁用的任务手动运行期间仍是 false，启用的任务运行中仍是 true。「启用 / 禁用」按钮应按它判断；老版本面板不下发这个字段，可回退为 status !== 0' },
        ],
      },
      {
        id: 'tasks-groups',
        method: 'GET',
        path: '/api/tasks/groups',
        title: '获取任务分组',
        description: '列出全部任务分组及各自的任务数，网页任务页的顶栏分组页签、任务表单「任务分组」下方的「已有分组」就用它。「分组」就是任务 labels 里的「分组:名称」标签（APP 的分组管理写的就是它）；一个任务有多个分组标签时只认第一个名字非空的。返回裸数组，按 name 升序（字节序）；一个分组都没有时返回 []。viewer 及以上角色可访问，应用令牌需要 tasks 权限。去重区分大小写（Prod 与 prod 算两个分组），而 filters 的 group 筛选不区分大小写。',
        auth: 'jwt',
        responseExample: JSON.stringify([
          { name: '日常', count: 5 },
          { name: '签到', count: 3 },
        ], null, 2),
        responseFields: [
          { name: '[].name', type: 'string', description: '分组名（去掉「分组:」前缀并 trim 后的名字）' },
          { name: '[].count', type: 'integer', description: '挂着这个分组的任务数' },
          { name: '按分组筛选', type: 'tip', description: '把 name 原样作为 filters 的 group 值传给 GET /api/tasks，例如 [{"field":"group","operator":"equals","value":"日常"}]' },
        ],
      },
      {
        id: 'tasks-labels',
        method: 'GET',
        path: '/api/tasks/labels',
        title: '获取任务标签',
        description: '列出全部任务用过的自定义标签及各自的任务数（v3.3.5 起），网页任务表单与「批量添加标签」下方的「已有标签」候选就用它。只含用户自己加的标签：每条先去掉首尾空白，以「分组:」或「subscription:」开头的两类内部标签不会出现（分组见 GET /api/tasks/groups；前缀不在开头的，如「my分组:beta」照常列出）。去掉首尾空白后去重计数，同一任务里重复的只算一次；去重区分大小写（Prod 与 prod 算两个）。返回裸数组，按 name 升序（字节序）；一个标签都没有时返回 []。不分页、不接受参数。viewer 及以上角色可访问，应用令牌需要 tasks 权限。查库失败返回 500「加载任务标签失败」。v3.3.4 及更早的面板没有这个接口（返回 404），调用方请按「没有候选」处理。',
        auth: 'jwt',
        responseExample: JSON.stringify([
          { name: '备份', count: 2 },
          { name: '监控', count: 4 },
        ], null, 2),
        responseFields: [
          { name: '[].name', type: 'string', description: '标签名（去掉首尾空白后的值）' },
          { name: '[].count', type: 'integer', description: '带这个标签的任务数，同一任务里重复的只算一次' },
          { name: '按常用排序', type: 'tip', description: '服务端只按 name 排；想把常用的放前面，客户端按 count 降序自己排（网页任务表单就是这么做的）' },
        ],
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
          { name: 'labels', type: 'array', description: '标签，字符串数组，原样保存：服务端不去首尾空白、不去重。存储时按英文逗号拼接，所以单个标签里的英文逗号会被拆成两个。任务分组也存在这里：「分组:名称」就是任务的分组（APP 的分组管理写的就是它，一个任务有多个时只认第一个名字非空的）；「subscription:ID」是订阅同步写的归属标签，不要手写。已有的自定义标签见 GET /api/tasks/labels', example: '["签到","分组:日常"]' },
        ],
        responseExample: JSON.stringify({ message: '创建成功', data: { id: 1, name: '签到任务', status: 1, enabled: true } }, null, 2),
        responseFields: [
          { name: 'data', type: 'object', description: '新任务的完整数据，含 enabled（新任务默认启用；示例只列出部分字段）' },
        ],
      },
      {
        id: 'tasks-update',
        method: 'PUT',
        path: '/api/tasks/:id',
        title: '更新任务',
        description: '更新指定任务的配置，需要 operator 及以上角色。只改请求里出现的字段，没出现的保持原值；labels 也一样：传了就用这一组整体替换原有的全部标签，不传则不动（见下）。name / command 不能传 null；cron_expression 在这次更新后是常规定时（cron）任务时也不能传 null：传了返回 400 并说明是哪个字段，整条更新都不写入（v3.3.4 及更早会整条不生效却照样回 200）。手动运行 / 开机运行任务（含同一个请求里把 task_type 改成 manual / startup）的 cron_expression 一律存成空串，传 null 也照常返回 200。请求体必须是 JSON 对象，传字面量 null 返回 400「请求参数错误」。写库失败返回 500「更新任务失败」，任务保持原样（v3.3.4 及更早同样照样回 200）。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        bodyParams: [
          { name: 'name', type: 'string', description: '任务名称。不能传 null（返回 400）' },
          { name: 'command', type: 'string', description: '执行命令。不能传 null（返回 400）' },
          { name: 'task_type', type: 'string', description: '任务类型：cron / manual / startup' },
          { name: 'cron_expression', type: 'string', description: 'Cron 表达式，只对常规定时（cron）任务生效。这次更新后是 cron 任务时不能传 null（返回 400）；手动运行 / 开机运行任务一律存成空串，传什么都一样（包括 null）' },
          { name: 'timeout', type: 'integer', description: '超时时间（秒），0 表示永不超时' },
          { name: 'max_retries', type: 'integer', description: '最大重试次数' },
          { name: 'retry_interval', type: 'integer', description: '重试间隔（秒）' },
          { name: 'notify_on_failure', type: 'boolean', description: '失败时通知' },
          { name: 'notify_on_success', type: 'boolean', description: '成功时通知' },
          { name: 'notify_on_abort', type: 'boolean', description: '主动终止时通知' },
          { name: 'notification_channel_id', type: 'integer', description: '指定通知渠道 ID，设为 null 则恢复为发送到全部「默认推送」渠道' },
          { name: 'labels', type: 'array', description: '标签，字符串数组，【整体覆写】：传了就用这一组替换原有的全部标签，不传则不动。「分组:名称」「subscription:ID」两类内部标签也在这一组里——只想改普通标签时要把原有的内部标签原样带上，漏了会把任务移出分组、切断它与订阅的关联（网页表单与 APP 都是这么做的）。只想追加标签请用 PUT /api/tasks/batch/add-labels。不去首尾空白、英文逗号会被拆开，规则同创建任务', example: '["签到","分组:日常"]' },
        ],
        responseExample: JSON.stringify({ message: 'task updated', data: { id: 1, name: '签到任务', status: 1, enabled: true } }, null, 2),
        responseFields: [
          { name: 'data', type: 'object', description: '任务的最新数据，含 enabled（示例只列出部分字段）' },
        ],
      },
      {
        id: 'tasks-copy',
        method: 'POST',
        path: '/api/tasks/:id/copy',
        title: '复制任务',
        description: '复制指定任务，需要 operator 及以上角色。副本名为「原名 (副本)」，命令、定时规则、标签、通知与重试等配置照搬，不带置顶与运行记录。副本一律是禁用状态、不会自动运行，确认无误后再调 PUT /api/tasks/:id/enable 启用。成功返回 201；任务不存在返回 404，写库失败返回 500「复制任务失败」。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '要复制的任务 ID' }],
        responseExample: JSON.stringify({ message: '复制成功', data: { id: 2, name: '签到任务 (副本)', status: 0, enabled: false } }, null, 2),
        responseFields: [
          { name: 'data', type: 'object', description: '副本的完整数据，含 enabled（恒为 false；示例只列出部分字段）' },
        ],
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
        id: 'tasks-batch-notify',
        method: 'PUT',
        path: '/api/tasks/batch/notify',
        title: '批量设置通知开关',
        description: '批量开启或关闭任务的失败 / 成功 / 终止通知（v3.3.3 起），需要 operator 及以上角色，应用令牌需要 tasks 权限。Web 任务页的「批量设置通知」与 APP 的批量通知走这里。只改请求里传了的开关，没传的保持各任务原值；传 false 会写成关闭。范围二选一：task_ids 指定任务，或 all=true 改全部任务（不受任何筛选影响；两者都传时以 all 为准）。不改各任务绑定的通知渠道。先检查开关、再检查范围：三个开关一个都没传返回 400「请至少设置一项通知开关」；all 不为 true 且 task_ids 为空返回 400「请先选择任务」；一个任务都没命中返回 404「没有找到要修改的任务」。v3.3.2 及更早的面板没有这个接口，会返回 404。',
        auth: 'jwt',
        bodyParams: [
          { name: 'task_ids', type: 'array', description: '任务 ID 数组。all 不为 true 时必填且不能为空；all=true 时忽略。不存在的 ID 不报错、不计数', example: '[12,13]' },
          { name: 'all', type: 'boolean', description: '为 true 时改全部任务，忽略 task_ids', example: 'false' },
          { name: 'notify_on_failure', type: 'boolean', description: '失败时通知。不传 = 不修改；必须是 JSON bool，传成字符串 "true" 会让整个请求返回 400「请求参数错误」', example: 'true' },
          { name: 'notify_on_success', type: 'boolean', description: '成功时通知，规则同 notify_on_failure' },
          { name: 'notify_on_abort', type: 'boolean', description: '主动终止时通知，规则同 notify_on_failure' },
        ],
        responseExample: JSON.stringify({ message: '已更新 2 个任务的通知设置', success_count: 2 }, null, 2),
        responseFields: [
          { name: 'success_count', type: 'integer', description: '命中的任务数：开关本来就是这个值的任务也计入，不存在的 ID 不计' },
          { name: 'message', type: 'string', description: '「已更新 {success_count} 个任务的通知设置」' },
          { name: '何时生效', type: 'tip', description: '已经在排队或正在执行的那一次仍用旧设置（执行时用的是入队那一刻读到的任务），下一次执行起生效；不需要重新保存任务，也不影响定时调度。与单个 PUT /api/tasks/:id 改通知开关的行为一致' },
          { name: '通知渠道', type: 'tip', description: '只改开关、不改渠道：绑定了渠道的任务仍只发到那一个渠道；没绑渠道的任务发到全部「默认推送」渠道（push_scope=default），设成「绑定推送」的渠道不参与' },
        ],
      },
      {
        id: 'tasks-batch-add-labels',
        method: 'PUT',
        path: '/api/tasks/batch/add-labels',
        title: '批量添加标签',
        description: '给多个任务追加标签，需要 operator 及以上角色，应用令牌需要 tasks 权限（网页任务页的「批量添加标签」走这里）。语义是【追加】：保留任务原有的全部标签（含分组与订阅标签），只把新标签并进去并去重；原有标签会顺带去掉首尾空白、去掉重复。待追加的标签逐条去掉首尾空白、跳过空串与重复，以「分组:」或「subscription:」开头的一律忽略（不能借它改分组）；一个有效标签都没有时返回 400「没有可添加的有效标签」，缺字段或类型不对返回 400「请求参数错误」。不存在的任务 ID 静默跳过。已有的自定义标签见 GET /api/tasks/labels。',
        auth: 'jwt',
        bodyParams: [
          { name: 'task_ids', type: 'array', required: true, description: '任务 ID 数组。不存在的 ID 不报错、不计数', example: '[12,13]' },
          { name: 'labels', type: 'array', required: true, description: '要追加的标签，字符串数组。存储时按英文逗号拼接，一个标签里别带英文逗号，否则会被拆成两个', example: '["签到","京东"]' },
        ],
        responseExample: JSON.stringify({ message: '已为 2 个任务添加标签', success_count: 2 }, null, 2),
        responseFields: [
          { name: 'success_count', type: 'integer', description: '实际写入成功的任务数：不存在的 ID、写库失败的任务都不计' },
          { name: 'message', type: 'string', description: '「已为 {success_count} 个任务添加标签」' },
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
        id: 'tasks-run-by-name',
        method: 'POST',
        path: '/api/tasks/run-by-name',
        title: '按名称执行任务',
        description: '按任务名精确匹配并立即执行，需要 operator 及以上角色。给「手上只有名字没有 ID」的调用方用（企业微信里回一句「运行 签到任务」走的就是这条）。匹配是精确相等、不是模糊搜索：「签到」不会命中「签到备份」。任务名在面板里没有唯一约束，重名是合法的，所以这里不会闷头跑第一条：一个都没匹配上返回 404「任务不存在」；匹配到多个返回 409，响应体里额外带 candidates 列出每个候选的 id 与 name，请改用 PUT /api/tasks/:id/run 精确指定。恰好命中一个时行为与 PUT /api/tasks/:id/run 完全一致（同一套实现），任务正在运行中返回 400「任务正在运行中」，入队失败返回 503。',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '任务名，精确匹配（首尾空格会被去掉）', example: '签到任务' },
        ],
        responseExample: JSON.stringify({ message: '任务已启动', data: { id: 1, name: '签到任务' } }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '固定为「任务已启动」' },
          { name: 'data', type: 'object', description: '命中的那个任务的 id 与 name' },
          { name: 'candidates', type: 'array', description: '仅在 409（重名）时出现，元素为 { id, name }' },
        ],
      },
      {
        id: 'tasks-stop',
        method: 'PUT',
        path: '/api/tasks/:id/stop',
        title: '停止任务',
        description: '停止排队中或正在运行的任务，需要 operator 及以上角色。停止时先给任务的整个进程组发 SIGTERM，5 秒后仍未退出再强制结束；接口立即返回。停止后按启用开关回到空闲或禁用，上次结果记为已终止。任务没在排队或运行（也没有残留进程）时返回 200 与「任务未在运行」，不改任何字段，上次结果也保持原样。任务不存在返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        responseExample: JSON.stringify({ message: '任务已停止' }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '一般为「任务已停止」；任务没在排队或运行时为「任务未在运行」' },
        ],
      },
      {
        id: 'tasks-enable',
        method: 'PUT',
        path: '/api/tasks/:id/enable',
        title: '启用任务',
        description: '打开任务的启用开关，需要 operator 及以上角色；cron 任务会重新注册到调度器，按定时规则自动运行。定时规则无效时返回 400，任务不存在返回 404。任务正在运行或排队中（例如禁用的任务被手动运行）时，本次执行不受影响、status 保持 2 或 0.5，结束后自动回到空闲；请以响应 data 里的 status 与 enabled 为准，不要自行把 status 改成 1。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        responseExample: JSON.stringify({ message: '已启用', data: { id: 1, name: '签到任务', status: 1, enabled: true } }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '固定为「已启用」' },
          { name: 'data', type: 'object', description: '任务的最新数据，含 enabled（示例只列出部分字段）' },
        ],
      },
      {
        id: 'tasks-disable',
        method: 'PUT',
        path: '/api/tasks/:id/disable',
        title: '禁用任务',
        description: '关闭任务的启用开关，需要 operator 及以上角色，禁用后不再按定时规则自动运行。任务正在运行时不会被中断：status 保持 2、enabled 立即变为 false，本次结束后落为禁用。任务不存在返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '任务 ID' }],
        responseExample: JSON.stringify({ message: '已禁用', data: { id: 1, name: '签到任务', status: 0, enabled: false } }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '一般为「已禁用」；任务正在运行时为「已设置为禁用，当前执行结束后生效」' },
          { name: 'data', type: 'object', description: '任务的最新数据，含 enabled（示例只列出部分字段）' },
        ],
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
        description: '获取所有脚本文件列表。所有 /api/scripts/* 接口（读取、保存、删除、改名、移动、复制、批量删除、回滚、调试运行等）的路径都相对脚本目录，并且一律拒绝下面这些目录，路径里任意一段命中都算、不区分大小写，返回 400 {"error":"该路径不可访问"}：.git、.svn、.hg、.bzr（版本库元数据，订阅用 Token 鉴权时 .git/config 里存着带访问令牌的地址，能读就等于泄露令牌，所以写死不可配置）；node_modules、__pycache__（依赖与字节码缓存，不属于脚本本身，脚本列表、统计与备份里也都忽略）。这是有意的安全限制，重试不会成功；确实要清理这些目录，可以用 POST /api/scripts/run-code 执行 shell 命令。',
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
        path: '/api/scripts?path=:path&type=:type',
        title: '删除脚本',
        description: '删除脚本目录里的一个文件，或整个目录（type=directory，连同里面的全部内容），不可恢复。删非空目录必须传 type=directory，否则返回 500「删除文件失败」。.git、node_modules、__pycache__ 这类被隔离的目录不能删除，返回 400 {"error":"该路径不可访问"}（原因见「获取脚本列表」）。',
        auth: 'jwt',
        queryParams: [
          { name: 'path', type: 'string', required: true, description: '文件或目录的相对路径（相对脚本目录）', example: 'demo/old.py' },
          { name: 'type', type: 'string', description: 'file（默认）或 directory', example: 'file' },
        ],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
      {
        id: 'scripts-run-code',
        method: 'POST',
        path: '/api/scripts/run-code',
        title: '执行代码片段',
        description: '不落盘直接执行一段代码，立即返回 run_id（HTTP 201），用「获取调试运行输出」轮询结果。language 区分大小写，可选 python、javascript、node（按 ES 模块 .mjs 执行）、mjs、typescript、shell、bash、go；bash 是 shell 的别名（v3.3.0 起），同样交给 bash 执行。工作目录是系统临时目录下的 daidai-debug，不是脚本目录，访问脚本请用绝对路径，脚本目录的绝对路径在环境变量 DAIDAI_SCRIPTS_DIR 里。需要 scripts 权限，等于能在面板上执行任意代码。',
        auth: 'jwt',
        bodyParams: [
          { name: 'code', type: 'string', required: true, description: '要执行的代码', example: 'ls -la "$DAIDAI_SCRIPTS_DIR"' },
          { name: 'language', type: 'string', required: true, description: '语言：python / javascript / node / mjs / typescript / shell / bash / go（区分大小写）', example: 'bash' },
        ],
        responseExample: JSON.stringify({ message: '代码已启动', run_id: 'code_1758160000000' }, null, 2),
        responseFields: [
          { name: 'run_id', type: 'string', description: '本次运行的 ID，用于取输出、停止与清除' },
          { name: '400', type: 'tip', description: 'language 不在上面的列表里时返回 {"error":"不支持的语言类型"}' },
        ],
      },
      {
        id: 'scripts-run-logs',
        method: 'GET',
        path: '/api/scripts/run/:run_id/logs',
        title: '获取调试运行输出',
        description: '读取 run-code 或调试运行（POST /api/scripts/run）的输出。done 为 true 表示已结束，此时 exit_code 与 status 为最终结果；短命令通常第一次读取就已结束。未结束时可用 PUT /api/scripts/run/:run_id/stop 停止。运行记录只保存在面板内存里，取完结果后可调 DELETE /api/scripts/run/:run_id 清除。已结束的记录最多保留 30 分钟；总数超过 20 条时，最早启动的已结束记录会先被清掉（运行中的不清）。清理发生在下一次启动调试运行 / 运行代码时，被清掉后再查返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'run_id', type: 'string', required: true, description: '执行代码片段 / 调试运行返回的 run_id' }],
        responseExample: JSON.stringify({ data: { logs: ['total 8', 'drwxr-xr-x 2 root root 4096 demo'], done: true, exit_code: 0, status: 'success' } }, null, 2),
        responseFields: [
          { name: 'data.logs', type: 'array', description: '输出行（标准输出与标准错误合在一起）' },
          { name: 'data.done', type: 'boolean', description: '是否已结束' },
          { name: 'data.exit_code', type: 'integer', description: '退出码，未结束时为 null' },
          { name: 'data.status', type: 'string', description: 'running / success / failed / stopped' },
        ],
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
        description: '获取环境变量列表，支持 keyword 模糊搜索与 names / groups / enabled 精确筛选（各条件之间是 AND），支持分页；带 all=1 时一次返回全部（硬上限 5000）。排列顺序：置顶区在前，区内按 position 升序；运行时同名变量（多账号）也按这个顺序用 & 拼接',
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
        responseExample: JSON.stringify({ data: [{ id: 1, name: 'MY_TOKEN', value: 'abc123', enabled: true, remarks: '签到Token', important: false }], total: 1 }, null, 2),
        responseFields: [
          { name: 'id', type: 'integer', description: '变量 ID' },
          { name: 'name', type: 'string', description: '变量名' },
          { name: 'value', type: 'string', description: '变量值' },
          { name: 'enabled', type: 'boolean', description: '是否启用' },
          { name: 'remarks', type: 'string', description: '备注' },
          { name: 'sort_order', type: 'integer', description: '所在区：1 为置顶区，0 为普通区，置顶区整体排在前面' },
          { name: 'position', type: 'number', description: '区内排序值，越小越靠前，置顶区与普通区各自比较。拖拽排序、置顶 / 取消置顶都会改写它，也可以用 PUT /api/envs/:id 直接填' },
          { name: 'important', type: 'boolean', description: '是否标为「重要」，v3.3.6 起每一项都带（false 也带）。只是给客户端做醒目显示与删除前确认的标记，服务端不拦删除' },
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
          { name: 'important', type: 'boolean', description: '新建时直接标为「重要」，默认 false；必须是布尔，否则返回 400', example: 'true' },
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
          { name: 'position', type: 'number', description: '区内排序值：越小越靠前，置顶区与普通区各自比较；只在值有变化时写入，必须是有限数字，否则返回 400。之后在列表里拖拽排序会把整区重新编号（步长 1000），手填的值会被覆盖。⚠️ 与 PUT /api/envs/sort 请求体里的 position（before / after 落点）同名不同义', example: '1500' },
          { name: 'important', type: 'boolean', description: 'true 标为「重要」、false 取消；不传不动，只在值有变化时写入；必须是布尔，否则返回 400', example: 'true' },
        ],
        responseExample: JSON.stringify({ message: '更新成功' }, null, 2),
      },
      {
        id: 'envs-delete',
        method: 'DELETE',
        path: '/api/envs/:id',
        title: '删除环境变量',
        description: '删除指定环境变量。标为「重要」的变量同样直接删除（这个标记只用于客户端的二次确认），调用前请自行确认',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '变量 ID' }],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
      {
        id: 'envs-sort',
        method: 'PUT',
        path: '/api/envs/sort',
        title: '调整环境变量顺序',
        description: '环境变量页的拖拽排序。把 source_id 挪到 target_id 前面（position=after 时挪到后面）；不传 target_id 表示移到本区末尾。「区」指置顶区与普通区，两区分别排序：跨区返回 400「置顶项和普通项请分别排序，需要跨区移动时请使用置顶按钮」，源或目标变量不存在返回 404。兄弟项取整个区而不是当前页，所以分页、筛选时拖拽同样有效；一次调用会把该区整体按 1000 的步长重新编号 position，另一个区不动。position 与 PUT /api/tasks/sort 同名同义。',
        auth: 'jwt',
        bodyParams: [
          { name: 'source_id', type: 'integer', required: true, description: '被拖动的变量 ID', example: '12' },
          { name: 'target_id', type: 'integer', description: '落点变量 ID；不传表示移到本区末尾', example: '34' },
          { name: 'position', type: 'string', description: '相对落点的位置：只认 after（插到目标之后，忽略大小写与首尾空格），不传或其它值一律按 before。⚠️ 与变量自身的数值字段 position（PUT /api/envs/:id 可写）同名不同义', example: 'after' },
        ],
        responseExample: JSON.stringify({ message: '排序更新成功' }, null, 2),
      },
      {
        id: 'envs-move-top',
        method: 'PUT',
        path: '/api/envs/:id/move-top',
        title: '置顶环境变量',
        description: '把变量移入置顶区，追加到置顶区末尾，所以先置顶的排在前面。对已经在置顶区的变量再调用，直接返回成功、位置不变。已置顶的变量彼此的顺序不会被重排，新置顶的一律排在它们后面。⚠️ 这个顺序同时是运行时同名变量（多账号）用 & 拼接的顺序，也就是脚本里的「第 N 个账号」。变量不存在返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '变量 ID' }],
        responseExample: JSON.stringify({ message: '已置顶' }, null, 2),
      },
      {
        id: 'envs-cancel-top',
        method: 'PUT',
        path: '/api/envs/:id/cancel-top',
        title: '取消置顶',
        description: '把变量移回普通区，追加到普通区末尾。对本来就不在置顶区的变量调用，同样会把它挪到普通区末尾。变量不存在返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '变量 ID' }],
        responseExample: JSON.stringify({ message: '已取消置顶' }, null, 2),
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
        description: '创建新的仓库订阅。白名单 / 黑名单 / 依赖规则里的正则片段会在保存时校验，写错返回 400 并点名字段、第几段和原因，例如「依赖规则第 2 段 `^jd[` 不是合法的正则表达式：missing closing ]: `[`（含 ^ $ ( ) [ ] { } ? \\ 或 .* .+ 的片段按正则解析，要写字面量请用 \\ 转义）」，此时订阅不会创建。',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '订阅名称' },
          { name: 'url', type: 'string', required: true, description: '仓库 URL（HTTP/HTTPS）' },
          { name: 'branch', type: 'string', description: '分支，默认 main', example: 'main' },
          { name: 'schedule', type: 'string', description: 'Cron 表达式', example: '0 0 * * *' },
          { name: 'whitelist', type: 'string', description: '白名单：多个片段用 , 或 | 分隔（配对的括号里、被 \\ 转义的 , | 不拆）。普通片段按「子串包含」匹配文件名 / 路径（不是 glob），片段命中目录名时该目录下的全部文件（含多级子目录）都算命中；含 ^ $ ( ) [ ] { } ? \\ 或 .* .+ 的片段按正则（Go RE2 语法）匹配仓库内的相对路径（不锚定，路径分隔写 /），例如 ^jd[^_] 只命中仓库根目录下的 jdCookie.js 这类文件，想按字面匹配这些字符请用 \\ 转义。命中的文件才会建成定时任务。检出范围：没填 sub_path 时，只有普通片段就只检出命中的文件，出现正则片段则改为检出完整仓库（建任务仍按白名单筛）；填了 sub_path 时按子目录检出，白名单只决定建不建任务。留空表示全部命中', example: 'jd_|jx_|jddj_' },
          { name: 'blacklist', type: 'string', description: '黑名单：写法与匹配规则同白名单（普通片段命中目录名时整个目录都被排除）。命中的文件不建任务；普通片段命中的文件也不检出，正则片段只影响建任务、文件仍会落盘', example: 'backUp' },
          { name: 'depend_on', type: 'string', description: '依赖规则：对应青龙 ql repo 的第 4 个参数 dependence，写法与匹配规则同白名单（填 utils 会把 utils/ 目录下的文件一并检出）。命中的文件会被检出落盘供主脚本调用，但不会建成定时任务（同时命中白名单的照常建任务；白名单留空时依赖规则不影响建任务）；含正则片段时改为检出完整仓库，填了 sub_path 也一样，但只给子目录里的脚本建定时任务。没填 sub_path 且白名单为空时本来就检出完整仓库，本字段不起作用', example: 'sendNotify|utils' },
          { name: 'sub_path', type: 'string', description: '只检出仓库内的指定子目录（多个用 , 或 | 分隔），按路径匹配、不认正则。优先级高于白名单：填了它，白名单就不参与检出，只决定建不建任务。依赖规则含正则片段时会改为检出完整仓库，但仍只给子目录里的脚本建定时任务' },
          { name: 'save_dir', type: 'string', description: '脚本存放子目录，留空则按仓库名推导' },
          { name: 'pre_script', type: 'string', description: '拉取前指令：在 git 拉取之前执行的 Shell 命令，可用 $SUB_DIR / $SCRIPTS_DIR / $QL_DIR 等变量。非 0 退出会中断本次拉取并记为失败', example: 'mount -a' },
          { name: 'hook_script', type: 'string', description: '拉取后钩子：拉取成功后、同步定时任务之前执行的 Shell 命令，变量与失败语义同 pre_script', example: 'bash $SUB_DIR/copyfiles.sh' },
          { name: 'overwrite_mode', type: 'string', description: '覆盖拉取策略，仅对 git 仓库订阅生效：inherit=跟随系统设置里的「覆盖拉取（默认）」开关（默认值）、force=强制覆盖本地改动、preserve=拉取前暂存本地改动再恢复。作用域只有脚本文件（git 工作区），不影响任务的名称与定时；首次拉取（本地还没有仓库）不适用。不传或传非法值一律按 inherit 处理', example: 'preserve' },
          { name: 'full_checkout', type: 'boolean', description: '完整检出，仅对 git 仓库订阅生效：true=放弃 sparse-checkout，拉取整个仓库（源码、资源、文档全部落盘，体积可能很大）；不传或 false=按「指定子目录 / 白名单 / 依赖规则」稀疏检出（默认）。不改变建任务的规则：仍然只有命中 sub_path 与白名单的文件会被建成定时任务。依赖规则有正则片段、或没填 sub_path 而白名单有正则片段时，默认就会检出完整仓库，但黑名单普通片段命中的文件仍不落盘；开启后它们也会落盘', example: 'true' },
          { name: 'auto_add_task_mode', type: 'string', description: '自动添加定时任务策略：inherit=跟随系统设置里的「自动添加定时任务（默认）」开关（默认值）、enabled=这条订阅强制建任务、disabled=这条订阅一律不建任务。作用域是拉取成功后「把命中的脚本同步成定时任务」这一步，不影响脚本文件的检出落盘；脚本未声明 cron 时按订阅设置里的默认 Cron 规则建，留空不建；与只对 git 仓库订阅生效的 overwrite_mode / full_checkout 不同，本项对单文件订阅同样生效。不传或传非法值一律按 inherit 处理', example: 'disabled' },
          { name: 'auto_del_task_mode', type: 'string', description: '自动删除失效任务策略：inherit=跟随系统设置里的「自动删除失效任务（默认）」开关（默认值）、enabled=这条订阅强制删除源文件已消失的任务、disabled=源文件没了也保留任务。作用域是带本订阅标签、且命令以 task 开头的任务，包括被本订阅关联的自建任务：脚本从订阅里消失（上游删除、被白/黑名单排除、改了保存目录）后会连日志删除；node / python3 / desi 等写法的任务不在自动删除范围；同时被其他订阅使用的任务只移除本订阅的标签、不删除。不想删自己的任务就设为 disabled。与 overwrite_mode / full_checkout 不同，本项对单文件订阅同样生效。不传或传非法值一律按 inherit 处理', example: 'enabled' },
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
        description: '更新指定订阅配置。请求体为部分更新，字段与「创建订阅」一致（含 whitelist / blacklist / depend_on / sub_path / overwrite_mode / full_checkout / auto_add_task_mode / auto_del_task_mode），只提交需要修改的字段即可。其中 overwrite_mode 的取值为 inherit / force / preserve，非法值会被归一成 inherit；full_checkout 为布尔值，true 表示拉取整个仓库、false（默认）表示按子目录/白名单/依赖规则稀疏检出；auto_add_task_mode / auto_del_task_mode 的取值为 inherit / enabled / disabled，非字符串或非法值同样归一成 inherit，且对单文件订阅一样生效。已废弃的 auto_add_task / auto_del_task **不在更新白名单里**，传了会被直接忽略（老客户端兼容靠的是响应里仍然下发这两个字段，不是靠可写）。白名单 / 黑名单 / 依赖规则只在提交的值与当前保存的值不同时才校验正则（没改动的旧值不会因此被拦下）；写错返回 400，同一请求里的其它字段也不会保存。',
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
        description: '创建新的通知渠道，支持：webhook / email / telegram(支持 api_host、proxy 单独代理) / dingtalk / wecom(企业微信机器人，支持 text/markdown/markdown_v2/image/news/template_card) / wecom_app(企业微信应用，支持 text/markdown/image/file/video/news/mpnews/template_card，并支持 base_url 反代基础地址 / 微信代理（留空使用 https://qyapi.weixin.qq.com，wxchat 这类微信代理填 http://VPS地址:端口；面板出口是动态 IP 时可以这样经 VPS 转发，再把 VPS 的 IP 加进企业可信 IP）、proxy 正向代理（http/https/socks5，留空使用系统代理）) / bark / pushplus(支持 channel 选择发送渠道：wechat/app/extension/webhook/clawbot/cp/qq/mail/sms/voice，webhook、cp 与 qq 需配合 option 填渠道编码；QQ 发给个人时 option 留空，发给群才填群配置编码) / serverchan / feishu(飞书机器人，webhook 必填、secret 加签可选；msg_type 选消息类型：text 文本 / post 富文本，标题加粗、正文按行分段 / interactive 卡片，标题显示在蓝色标题栏，默认 text，三种都支持加签；发送时声明 content_type=markdown 不改消息类型，只让卡片正文按 lark_md 渲染) / gotify / pushdeer / pushme / chanify / igot / qmsg / pushover / discord / slack / ntfy / wxpusher(WxPusher / ClawBot(iLink)，支持 url、verify_pay_type) / custom',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '渠道名称' },
          { name: 'type', type: 'string', required: true, description: '渠道类型', example: 'dingtalk' },
          { name: 'config', type: 'object', required: true, description: '渠道配置（各类型字段不同，例如 email 可填 smtp_ssl，telegram 可填 proxy，wecom_app 可填 base_url / proxy，wxpusher 可填 url / verify_pay_type，feishu 可填 msg_type = text / post / interactive，不填按 text）' },
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
        description: '供脚本或外部程序主动调用系统通知配置进行推送。支持普通用户 JWT，也支持带 notifications scope 的 Open API Bearer Token。未指定 channel_id / channel_ids 时走广播，只会发送到「默认推送」（push_scope=default）且已启用的渠道，设为「绑定推送」的渠道不会收到；显式指定渠道 ID 时按 ID 精确投递，绑定推送渠道同样能收到。一个「默认推送」渠道都没有时不做兜底，直接返回失败。任务自带默认通知渠道时，helper 会优先落到该渠道，传 ignore_default_config=true 可跳过它 —— 注意跳过后就是走广播，也就是只发到「默认推送」渠道，而不是发给所有渠道。面板运行脚本时会统一在脚本根目录提供 notify.py 和 sendNotify.js，不再向每个脚本子目录复制，可直接按青龙风格先收集 notifyStr 再发送。可用 content_type 声明正文是 text / markdown / html（不传则按渠道配置发送，与以前一致）：邮件发 text/html、WxPusher 按 HTML 渲染、不支持 HTML 的渠道自动去掉标签；用 channel_name(s) 按名称点名、channel_type(s) 按渠道类型过滤，notify.py 另有与青龙同名的分渠道函数（wxpusher_bot、smtp 等，没有匹配渠道时打印一行跳过、不抛异常）和 send_to，sendNotify.js 有 sendTo。',
        auth: 'jwt',
        bodyParams: [
          { name: 'title', type: 'string', required: true, description: '通知标题', example: '签到脚本通知' },
          { name: 'content', type: 'string', required: true, description: '通知正文，通常传 notifyStr.join(\'\\n\') 或 "\\n".join(notify_lines)', example: '签到成功\\n账号: user01\\n积分: +20' },
          { name: 'channel_id', type: 'integer', description: '单个通知渠道 ID，可选；传了就必须大于 0，否则返回 400（不再静默退化成广播）', example: '3' },
          { name: 'channel_ids', type: 'array', description: '多个通知渠道 ID，可选；非空数组里必须至少有一个大于 0 的 ID，否则返回 400。空数组等同不传，按广播处理', example: '[3,5]' },
          { name: 'channel_name', type: 'string', description: '按渠道名称点名，可选；与 channel_id 同级（绑定推送渠道也能收到），名称不存在返回 400', example: '我的邮箱' },
          { name: 'channel_names', type: 'array', description: '多个渠道名称，可选；任一名称不存在返回 400 并列出，不会退化成广播', example: '["我的邮箱","WxPusher"]' },
          { name: 'channel_type', type: 'string', description: '按渠道类型过滤，可选，大小写不限。可选值：webhook、email、telegram、dingtalk、wecom（企业微信机器人）、wecom_app（企业微信应用）、bark、pushplus、serverchan（Server酱）、feishu、gotify、pushdeer、pushme、chanify、igot、qmsg、pushover、discord、slack、ntfy、wxpusher、custom（自定义）；写错返回 400 并列出全部可选值。点名了渠道时取交集，未点名时只在「默认推送」渠道里过滤', example: 'wxpusher' },
          { name: 'channel_types', type: 'array', description: '多个渠道类型，可选；规则同 channel_type', example: '["email","telegram"]' },
          { name: 'content_type', type: 'string', description: '正文格式，可选：text / markdown / html（也认 text/html、text/plain、text/markdown 这类写法，大小写不限），不传则按各渠道配置发送。html 时邮件发 text/html、WxPusher 不转义、PushPlus 用 html 模板，不支持 HTML 的渠道去标签转纯文本；markdown 不会把企业微信应用的文本消息改成 markdown 消息（微信插件里看不到），企业微信机器人配了 @ 成员时同样保持文本。注意：这与渠道配置里 wxpusher / custom 的同名配置项不是一回事', example: 'html' },
          { name: 'context', type: 'object', description: '额外模板变量，可选；供 content_template 使用', example: '{"task_name":"签到脚本","status":"success"}' },
        ],
        helperExamples: {
          JavaScript: `const { sendNotify, sendTo } = require('./sendNotify')

async function main() {
  const name = '京东签到'
  const notifyStr = []
  notifyStr.push('签到成功')
  notifyStr.push('账号: user01')
  notifyStr.push('积分: +20')
  await sendNotify(name, notifyStr.join('\\n'))

  // 如需忽略任务默认通知渠道：
  // await sendNotify(name, notifyStr.join('\\n'), { channel_ids: [1, 2], ignore_default_config: true })

  // 正文是 HTML / 按渠道发不同内容：
  // await sendTo('wxpusher', name, '<table>...</table>', { content_type: 'html' })
  // await sendNotify(name, notifyStr.join('\\n'), { channel_name: '我的邮箱' })
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

    # 正文是 HTML / 按渠道发不同内容（与青龙同名的分渠道函数，其它渠道类型用 send_to）：
    # import notify
    # notify.wxpusher_bot(name, '<table>...</table>', content_type='html')
    # notify.smtp(name, '\\n'.join(notify_lines))
    # notify.send_to('discord', name, '\\n'.join(notify_lines))

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
            content_type: '',
            channel_types: [],
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
      {
        id: 'deps-playwright-status',
        method: 'GET',
        path: '/api/deps/playwright',
        title: '查询 Playwright 运行环境',
        description: '查询当前部署能不能一键安装 Playwright 运行环境，以及三样东西各自装好了没有：Linux 系统库、Python 的 playwright 包、Chromium 浏览器。依赖管理页 Linux 页签的「安装 Playwright 运行环境」按钮就是按它决定是否置灰、是否显示「已就绪」。只读，不会触发任何安装。仅管理员可调（/deps 整组都是），不对 Open API 应用开放。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          supported: true,
          reason: '',
          distribution: 'debian',
          version_id: '12',
          arch: 'amd64',
          packages: [
            'libasound2', 'libatk-bridge2.0-0', 'libatk1.0-0', 'libatspi2.0-0', 'libcairo2', 'libcups2', 'libdbus-1-3',
            'libdrm2', 'libgbm1', 'libglib2.0-0', 'libnspr4', 'libnss3', 'libpango-1.0-0', 'libx11-6', 'libxcb1',
            'libxcomposite1', 'libxdamage1', 'libxext6', 'libxfixes3', 'libxkbcommon0', 'libxrandr2',
            'fonts-liberation', 'fonts-wqy-zenhei', 'fonts-noto-color-emoji', 'libfontconfig1', 'libfreetype6',
          ],
          browsers_path: '/app/Dumb-Panel/deps/ms-playwright',
          python_installed: true,
          browsers_installed: true,
          linux_installed: 26,
          linux_total: 26,
        }, null, 2),
        responseFields: [
          { name: 'supported', type: 'boolean', description: '当前部署是否支持一键安装。只有 Debian 12（bookworm）+ apt + amd64 / arm64、在 Docker 容器里运行、且面板进程是 root 时才是 true（也就是以 root 运行的 Debian 版 Docker 镜像）。Alpine（musl 跑不了官方的 glibc 版 Chromium）、其它发行版或版本、非 Linux、其它 CPU 架构、非容器部署（裸机 Debian 12 或面具模块版：浏览器目录不由面板托管）、面板进程不是 root（比如设了 PUID 降权运行）时一律 false，原因见 reason。它与安装接口的前置检查是同一套规则：这里是 true，安装接口就不会因为环境或权限回 400' },
          { name: 'reason', type: 'string', description: '不支持时的原因，中文，可直接展示给用户：发行版或版本不匹配时会带上探测到的 os-release 信息；非 root 时是权限说明（与安装接口 400 的文案相同）。supported=true 时为空串' },
          { name: 'distribution', type: 'string', description: '探测到的发行版，即 /etc/os-release 的 ID，如 debian / ubuntu / alpine' },
          { name: 'version_id', type: 'string', description: '发行版版本，即 /etc/os-release 的 VERSION_ID，如 12' },
          { name: 'arch', type: 'string', description: '面板进程的 CPU 架构，如 amd64 / arm64' },
          { name: 'packages', type: 'array', description: '一键安装会登记为 Linux 依赖的系统包：Chromium 必需的 21 个运行库，加上 5 个字体相关的包（含中文字体 fonts-wqy-zenhei，不装的话中文页面截图是方块），共 26 个，amd64 与 arm64 相同。不含 xvfb，那只有有头模式才用得到' },
          { name: 'browsers_path', type: 'string', description: 'Chromium 的下载目录，也就是任务里 PLAYWRIGHT_BROWSERS_PATH 的实际取值。优先级：环境变量页里启用的同名变量 > 面板进程的环境变量 > 默认值。Docker 部署的默认值是数据目录下的 deps/ms-playwright（默认 /app/Dumb-Panel/deps/ms-playwright），在数据卷里，重建容器不丢；Windows、裸机与 Magisk 部署没有默认值' },
          { name: 'python_installed', type: 'boolean', description: '默认 Python 版本下的 playwright 是否已登记为已安装的 Python 依赖、且 pip 里确实装着，两项都满足才为 true。在面板外手动 pip 装的（没有登记）、以及正在排队 / 安装 / 卸载中的（包括一键安装刚把它重新入队）都返回 false' },
          { name: 'browsers_installed', type: 'boolean', description: 'browsers_path 下是否已经有 Chromium（存在 chromium 开头的目录即算）。只看目录在不在，不核对版本' },
          { name: 'linux_installed', type: 'integer', description: 'packages 里已登记为 Linux 依赖、且系统里确实装着的包数。容器重建后系统库丢失、正在后台自动重装时，这个数会暂时小于 linux_total' },
          { name: 'linux_total', type: 'integer', description: '需要的系统包总数，即 packages 的长度' },
        ],
      },
      {
        id: 'deps-playwright-install',
        method: 'POST',
        path: '/api/deps/playwright/install',
        title: '一键安装 Playwright 运行环境',
        description: '依赖管理页 Linux 页签的「安装 Playwright 运行环境」按钮调的就是它，不需要请求体。一次做三件事：① 把 packages 里的系统包逐个登记成 Linux 依赖（已登记且已装好的跳过，之前失败或取消的复用原记录）；② 在默认 Python 版本下登记 playwright 包，已经装过也会重新排队，用来补下载浏览器；③ pip 装好之后，在同一条依赖记录、同一份日志里接着执行 python -m playwright install chromium，把浏览器下载到 browsers_path。所有记录按「先系统库、后 Python」的顺序进同一个队列依次安装，接口只负责入队、立即返回，进度和日志照常在依赖列表里看。因为系统库已登记成 Linux 依赖，容器重建后面板的启动校验会在后台自动重装它们；浏览器与 pip 包在数据卷里，不需要重装。⚠️ 前置检查全部在建任何记录之前完成：不支持（非 Linux、Alpine / apk、非 apt、非 Debian 12、非 amd64 / arm64、非 Docker 部署——裸机 / 面具模块版）或面板进程不是 root 时直接回 400，不会留下一批失败记录；GET /deps/playwright 已按同样的规则给出 supported=false 与原因，调用前可以先查。仅管理员可调，不对 Open API 应用开放。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          '已加入安装队列 (201)': {
            message: '已加入安装队列，共 27 项',
            data: [
              { id: 31, type: 'linux', name: 'libasound2', status: 'queued' },
              { id: 57, type: 'python', name: 'playwright', python_version: '3.12', status: 'queued' },
            ],
            packages: ['libasound2', 'libatk-bridge2.0-0', 'libatk1.0-0'],
            browsers_path: '/app/Dumb-Panel/deps/ms-playwright',
          },
          '不支持当前环境 (400)': {
            error: 'Alpine 镜像（musl）跑不了 Playwright 官方的 Chromium（glibc 构建），请把镜像标签里的 latest 换成 debian（如 latest → debian、latest-full → debian-full）',
          },
        }, null, 2),
        responseFields: [
          { name: 'message', type: 'string', description: '入队结果的提示文案' },
          { name: 'data', type: 'array', description: '本次加入队列的依赖记录（系统包 + playwright），字段同「获取依赖列表」的 data[]，status 为 queued（示例里截短了）。没有需要入队的项时仍回 201，data 为 []，message 会写明跳过的项数。playwright 那条记录只要不在处理中就会重新入队，所以 data 为空实际意味着上一轮还在排队或安装中' },
          { name: 'packages', type: 'array', description: '系统包清单，同「查询 Playwright 运行环境」的 packages（示例里截短了）' },
          { name: 'browsers_path', type: 'string', description: 'Chromium 将要下载到的目录，同「查询 Playwright 运行环境」的 browsers_path' },
          { name: 'error', type: 'string', description: '失败原因（400 / 500 时返回），中文，可直接展示：400 时，不支持的给出具体原因，非 root 的说明需要以 root 运行；500 表示登记依赖记录时数据库出错（文案「登记 Playwright 依赖失败，请稍后重试」），本次新建的记录已回滚，可以直接重试' },
        ],
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
          { name: 'password', type: 'string', required: true, description: '密码：8-128 位，且字母、数字、符号至少包含两种' },
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
        id: 'users-reset-password',
        method: 'PUT',
        path: '/api/users/:id/reset-password',
        title: '重置用户密码',
        description: '管理员直接给指定用户设一个新密码（仅管理员）。成功后该用户所有登录都会被撤销、需要用新密码重新登录，并会一并清除该用户的通行密钥。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '用户 ID' }],
        bodyParams: [
          { name: 'password', type: 'string', required: true, description: '新密码：8-128 位，且字母、数字、符号至少包含两种' },
        ],
        responseExample: JSON.stringify({ message: '密码已重置' }, null, 2),
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
        description: '使用 App Key 和 App Secret 获取访问令牌，有效期 24 小时。⚠️ 开放 API（REST）的鉴权与 MCP 不同：REST 接口只认 Bearer 令牌，必须先调本接口换取 access_token（expires_in 为 86400 秒），再在请求头带 Authorization: Bearer <access_token>。把 app_secret 直接当 Bearer 令牌会返回 401 {"error":"令牌无效或已过期"}；REST 接口也不接受 Basic，带 Basic 或不带 Authorization 都返回 401 {"error":"缺少授权令牌"}。只有 MCP 接口（/api/v1/mcp）可以直接用 Authorization: Basic base64(app_key:app_secret)。令牌里带着应用勾选的权限范围，调用没勾选的模块返回 403 {"error":"应用无权访问此资源"}。同一 IP 10 分钟内凭证错误超过 10 次会被暂时拒绝，返回 429，凭证正确的请求不计次数。',
        auth: 'none',
        bodyParams: [
          { name: 'app_key', type: 'string', required: true, description: 'App Key' },
          { name: 'app_secret', type: 'string', required: true, description: 'App Secret' },
        ],
        responseExample: JSON.stringify({ data: { access_token: 'eyJhbGciOi...', token_type: 'Bearer', expires_in: 86400 } }, null, 2),
      },
      {
        id: 'open-task-trigger-link',
        method: 'POST',
        path: '/api/open-api/apps/:id/task-trigger-link',
        title: '生成企业微信触发链接',
        description: '为某个任务签发一条「点一下就执行」的长期链接，用来配到企业微信自建应用的菜单里（issue #145）。需要管理员登录令牌。返回的 url 是完整地址（按当前请求的 Host 与 X-Forwarded-Proto 推导），path 是同一条地址的相对形式。⚠️ 这条链接长期有效且无需登录，等同于这一个任务的永久触发权，只应配置到企业微信菜单里，不要贴进聊天或工单；它会留在企业微信服务器、内置浏览器历史与反向代理访问日志里。票据用 HMAC-SHA256 签名，签名原文里含任务 ID，所以改 URL 上的 task_id 一定验签失败，一张票挪不到别的任务上。需要止血时调用「作废全部企业微信触发链接」。应用不存在返回 404「应用不存在」，任务不存在返回 404「任务不存在」。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '开放 API 应用 ID，仅用于记录这条链接是以哪个应用的名义签发的' }],
        bodyParams: [{ name: 'task_id', type: 'integer', required: true, description: '要触发的任务 ID；票据只绑这一个任务' }],
        responseExample: JSON.stringify({
          data: {
            url: 'https://panel.example.com/api/v1/open-api/trigger?task_id=1&ticket=v1.YWRtaW4.0.0.3Qk8x1r0Zq2m9v7bN4pS6cW8dY0aE2gH5jK7lM9nQ1s',
            path: '/api/v1/open-api/trigger?task_id=1&ticket=v1.YWRtaW4.0.0.3Qk8x1r0Zq2m9v7bN4pS6cW8dY0aE2gH5jK7lM9nQ1s',
            task_id: 1,
            task_name: '签到任务',
            app_name: '企业微信',
            notice: '该链接长期有效且无需登录，请只配置到企业微信应用菜单里；需要作废时调用「作废全部触发链接」',
          },
        }, null, 2),
        responseFields: [
          { name: 'url', type: 'string', description: '完整触发地址，直接填进企业微信应用的菜单配置' },
          { name: 'path', type: 'string', description: '同一条地址的相对形式，前缀跟着签发时用的 /api 或 /api/v1 走' },
          { name: 'notice', type: 'string', description: '给用户看的风险提示，建议原样展示在复制按钮旁' },
        ],
      },
      {
        id: 'open-task-trigger-links-revoke',
        method: 'POST',
        path: '/api/open-api/task-trigger-links/revoke',
        title: '作废全部企业微信触发链接',
        description: '把触发链接的「代次」+1，所有已经发出去的链接立刻失效（再打开返回 401「链接已失效」）。需要管理员登录令牌。作废是全局的、不可逐条撤销 —— 触发票据是无状态的、不落库，只能靠代次整批失效，作废后需要为仍在用的菜单重新生成链接。代次存在系统配置里，面板重启不会丢。',
        auth: 'jwt',
        responseExample: JSON.stringify({ message: '已作废全部企业微信触发链接', data: { generation: 1 } }, null, 2),
        responseFields: [
          { name: 'generation', type: 'integer', description: '作废之后的代次，只用于排查，不需要保存' },
        ],
      },
      {
        id: 'open-task-trigger',
        method: 'GET',
        path: '/api/open-api/trigger',
        title: '凭链接触发任务',
        description: '企业微信菜单链接真正落地的那一下。这条路由刻意没有挂 JWT 中间件，只认 URL 上的 ticket —— 企业微信内置浏览器打开菜单链接时带不了 Authorization 头；换票接口是 POST /api/open-api/apps/:id/task-trigger-link（需要管理员），不是同路径的 -ticket 形式。防线是三层：①「系统设置 → 企业微信触发」的总开关未开返回 403；② 票据验签（无效、被改过 task_id、已被作废三种情况一律返回 401「链接已失效」，不区分原因以免被拿来探测）；③「允许企业微信触发执行」未开返回 403。三关都过才会执行任务，执行语义与 PUT /api/tasks/:id/run 完全一致。响应是一小段 HTML 而不是 JSON（企业微信会在内置浏览器里直接打开它），页面上只有一行「已触发：<任务名>」或失败原因，带 Cache-Control: no-store。',
        auth: 'ticket',
        queryParams: [
          { name: 'task_id', type: 'integer', required: true, description: '任务 ID，必须与签发时一致，改了一定验签失败', example: '1' },
          { name: 'ticket', type: 'string', required: true, description: '签发接口返回的票据（已包含在返回的 url 里，不要手动拼）', example: 'v1.YWRtaW4.0.0.3Qk8...' },
        ],
        responseContentType: 'text/html; charset=utf-8',
        responseExample: `HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
Cache-Control: no-store, no-cache, must-revalidate
X-Content-Type-Options: nosniff

<!DOCTYPE html><html lang="zh-CN">...<div>已触发</div><div>签到任务</div>...</html>`,
      },
      {
        id: 'wecom-triggers-list',
        method: 'GET',
        path: '/api/wecom/triggers',
        title: '获取企业微信接入配置',
        description: '列出全部企业微信接入配置（issue #145）。需要管理员登录令牌 —— 这一组是普通的管理接口，和同层免鉴权的 /api/wecom/callback/:id 只是共用一个 /wecom 前缀。⚠️ Token 与 EncodingAESKey 不下发：这两项泄漏任意一个都等于把公网入口交出去，只允许写入、任何接口都不回显，所以响应里没有它们。callback_path 不是库里的字段，是服务端按当前请求前缀拼好的回调路径（/api 与 /api/v1 两套都能自动对上），把它接在面板对外地址后面就是要填进企业微信后台的回调 URL。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          data: [{
            id: 1,
            name: '运维群自建应用',
            corp_id: 'ww1a2b3c4d5e6f7g8h',
            agent_id: '1000002',
            open_app_id: 2,
            task_whitelist: '',
            enabled: true,
            callback_path: '/api/v1/wecom/callback/1',
            created_at: '2026-09-12T10:00:00+08:00',
            updated_at: '2026-09-19T15:30:00+08:00',
          }],
        }, null, 2),
        responseFields: [
          { name: 'open_app_id', type: 'integer', description: '回放 /tasks 接口时用的开放 API 应用 ID，它的权限范围必须含 tasks' },
          { name: 'task_whitelist', type: 'string', description: '允许被触发的任务白名单，留空 = 不限制' },
          { name: 'callback_path', type: 'string', description: '服务端拼好的回调路径，前缀跟着本次请求走' },
        ],
      },
      {
        id: 'wecom-triggers-create',
        method: 'POST',
        path: '/api/wecom/triggers',
        title: '创建企业微信接入配置',
        description: '新建一套「企业微信自建应用 → 面板」的接入配置（issue #145），需要管理员登录令牌。创建成功后响应里带 callback_path，把它接在面板对外地址后面填进企业微信后台的「接收消息服务器配置」即可。两处校验会直接 400：EncodingAESKey 不是 43 位（长度不对根本解不出密钥）；绑定的开放 API 应用不存在，或没有勾选「tasks」权限范围（没有 tasks 的应用铸出来的令牌会在回放时被 403，表现是「指令发了但任务没跑、界面上什么都看不到」，所以在保存时就拦掉）。',
        auth: 'jwt',
        bodyParams: [
          { name: 'name', type: 'string', required: true, description: '配置名称，只用于在面板里区分多套接入', example: '运维群自建应用' },
          { name: 'corp_id', type: 'string', required: true, description: '企业微信的企业 ID。解密后会拿它校验消息归属，对不上一律丢弃 —— 这是这条公网回调唯一的身份锚点', example: 'ww1a2b3c4d5e6f7g8h' },
          { name: 'agent_id', type: 'string', description: '自建应用的 AgentID，可留空（按字符串存，与通知渠道 wecom_app 保持一致）', example: '1000002' },
          { name: 'callback_token', type: 'string', required: true, description: '企业微信后台「接收消息服务器配置」里的 Token。只写不读，任何接口都不回显' },
          { name: 'encoding_aes_key', type: 'string', required: true, description: '企业微信后台给出的 43 位 EncodingAESKey，长度不对返回 400。只写不读，任何接口都不回显' },
          { name: 'open_app_id', type: 'integer', required: true, description: '绑定的开放 API 应用 ID，权限范围必须含 tasks', example: '2' },
          { name: 'task_whitelist', type: 'string', description: '允许被触发的任务白名单，用逗号（半角或全角）或换行分隔，每项可写任务 ID 也可写任务名。指令用哪种形式就按哪种形式匹配：「run 12」拿 ID 比、「运行 签到任务」拿名字比。留空 = 不限制', example: '12,签到任务' },
          { name: 'enabled', type: 'boolean', description: '是否启用，默认 true。禁用时该条回调直接返回 403' },
        ],
        responseExample: JSON.stringify({
          message: '创建成功',
          data: { id: 1, name: '运维群自建应用', corp_id: 'ww1a2b3c4d5e6f7g8h', agent_id: '1000002', open_app_id: 2, task_whitelist: '', enabled: true, callback_path: '/api/v1/wecom/callback/1' },
        }, null, 2),
      },
      {
        id: 'wecom-triggers-update',
        method: 'PUT',
        path: '/api/wecom/triggers/:id',
        title: '更新企业微信接入配置',
        description: '按键更新：请求里没出现的键一概不动已有值（与 PUT /api/notifications/:id 同一口径，独立发版的 APP 不带某个字段时不会把它清掉）。callback_token 与 encoding_aes_key 额外多一条 —— 传了但是空串一律当「不改」，因为前端的密码框回显不出原值、保存时带的就是空串，按字面写回去会把配置改坏且没有任何提示。校验拿更新后的值来做：没传的字段沿用库里的现值，所以只改名称也会连带校验一次「绑定的应用是否仍然存在、是否仍有 tasks 权限」。需要管理员登录令牌，配置不存在返回 404。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '接入配置 ID' }],
        bodyParams: [
          { name: 'name', type: 'string', description: '配置名称' },
          { name: 'corp_id', type: 'string', description: '企业 ID' },
          { name: 'agent_id', type: 'string', description: '自建应用 AgentID' },
          { name: 'callback_token', type: 'string', description: 'Token；留空或不传都表示不修改' },
          { name: 'encoding_aes_key', type: 'string', description: '43 位 EncodingAESKey；留空或不传都表示不修改' },
          { name: 'open_app_id', type: 'integer', description: '绑定的开放 API 应用 ID，必须是正整数且该应用含 tasks 权限' },
          { name: 'task_whitelist', type: 'string', description: '任务白名单，留空 = 不限制' },
          { name: 'enabled', type: 'boolean', description: '是否启用' },
        ],
        responseExample: JSON.stringify({
          message: '更新成功',
          data: { id: 1, name: '运维群自建应用', corp_id: 'ww1a2b3c4d5e6f7g8h', agent_id: '1000002', open_app_id: 2, task_whitelist: '12,签到任务', enabled: true, callback_path: '/api/v1/wecom/callback/1' },
        }, null, 2),
      },
      {
        id: 'wecom-triggers-delete',
        method: 'DELETE',
        path: '/api/wecom/triggers/:id',
        title: '删除企业微信接入配置',
        description: '删除一条接入配置（需要管理员登录令牌）。删除后对应的回调路由立刻返回 404，企业微信后台配的那条 URL 随之失效，需要重新创建并重新配置。',
        auth: 'jwt',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '接入配置 ID' }],
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
      {
        id: 'wecom-callback-verify',
        method: 'GET',
        path: '/api/wecom/callback/:id',
        title: '企业微信回调 URL 验签',
        description: '企业微信后台「接收消息服务器配置」点保存时会先发这一次 GET 来验证 URL。面板校验 msg_signature（token / timestamp / nonce / echostr 四个串排序后拼接取 sha1，常量时间比较）、解密 echostr、并校验明文尾部的 receiveid 与本接入配置的 CorpID 一致，全部通过后把解密出的 echostr 原样回成纯文本（不是 JSON，企业微信只认裸文本）。路由本身是公开的（企业微信服务器带不了任何面板凭据），但「系统设置 → 企业微信触发」的总开关默认关闭，关着时这里直接返回 403、连验签都不做 —— 与 /api/v1/mcp 是同一套「路由公开 + handler 内强鉴权」口径。前置条件：企业微信服务器要能访问到面板（公网可达），建议使用 80/443 端口，并在企业微信后台配好可信 IP。接入配置不存在或已禁用返回 404/403，验签或解密失败返回 401。接入配置本身（CorpID / AgentID / Token / EncodingAESKey / 绑定的开放 API 应用 / 任务白名单）用管理员登录令牌通过 GET、POST /api/wecom/triggers 与 PUT、DELETE /api/wecom/triggers/:id 维护 —— 那一组是普通的管理接口，别因为共用 /wecom 前缀就以为整段都免鉴权；Token 与 EncodingAESKey 只允许写入、任何接口都不回显，创建与更新时响应里会带上拼好的 callback_path，直接复制到企业微信后台即可。',
        auth: 'none',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '面板里那条企业微信接入配置的 ID' }],
        queryParams: [
          { name: 'msg_signature', type: 'string', required: true, description: '企业微信给出的签名' },
          { name: 'timestamp', type: 'string', required: true, description: '时间戳' },
          { name: 'nonce', type: 'string', required: true, description: '随机串' },
          { name: 'echostr', type: 'string', required: true, description: '加密后的随机串，验证通过后要原样回解密结果' },
        ],
        responseContentType: 'text/plain; charset=utf-8',
        responseExample: `HTTP/1.1 200 OK
Content-Type: text/plain; charset=utf-8

1616140317555161061`,
      },
      {
        id: 'wecom-callback-receive',
        method: 'POST',
        path: '/api/wecom/callback/:id',
        title: '企业微信消息回调（拉起任务）',
        description: '企业微信把用户在应用里发的消息投到这里。面板先验 msg_signature、再用 EncodingAESKey 解密、再强制校验明文尾部 receiveid 与 CorpID 一致（这一步没有开关，漏了就等于任何人都能拉起任务），然后从文字消息的 Content 或菜单点击事件的 EventKey 里解指令。支持四种写法：「运行 签到任务」「运行签到任务」「run 12」「run 签到任务」；纯数字一律当任务 ID。命中后用接入配置绑定的开放 API 应用现铸一枚短期令牌，在进程内回放 PUT /api/v1/tasks/:id/run 或 POST /api/v1/tasks/run-by-name —— 也就是说 scope 校验、调用频率限制与「Open API」页面的调用日志全部照常生效，这条入口不比开放 API 多任何权限。还受两道闸门约束：「允许企业微信触发执行」未开时只验签不执行；每条接入配置可以再配任务白名单（留空=不限制）。回包口径：验签之前的失败按真实状态码回（403/404/401），验签之后一律回 200 + 空串 —— 企业微信要求 5 秒内响应、非 200 会重试三次，重试意味着同一条指令被执行三遍；任务执行结果走现有的「企业微信应用」通知渠道异步推回，不占这次响应。',
        auth: 'none',
        pathParams: [{ name: 'id', type: 'integer', required: true, description: '面板里那条企业微信接入配置的 ID' }],
        queryParams: [
          { name: 'msg_signature', type: 'string', required: true, description: '企业微信给出的签名' },
          { name: 'timestamp', type: 'string', required: true, description: '时间戳' },
          { name: 'nonce', type: 'string', required: true, description: '随机串' },
        ],
        bodyParams: [
          { name: '(XML)', type: 'string', required: true, description: '企业微信的加密信封 <xml><ToUserName/><AgentID/><Encrypt/></xml>，请求体上限 64KB' },
        ],
        responseContentType: 'text/plain; charset=utf-8（空串）',
        responseExample: `HTTP/1.1 200 OK
Content-Type: text/plain; charset=utf-8
Content-Length: 0`,
      },
    ],
  },
  {
    key: 'mcp',
    label: 'MCP',
    endpoints: [
      {
        id: 'mcp-endpoint',
        method: 'POST',
        path: '/api/v1/mcp',
        title: '连接 MCP 服务',
        description: '面板内置的 MCP（Model Context Protocol）服务：Claude Desktop、Cursor、Cherry Studio 等 AI 客户端连上后可以查任务、看日志、巡检失败任务，管理员放开写入后还能新建和修改任务、改环境变量、管理脚本文件、执行代码片段、管理订阅、发通知、备份与恢复，能力与开放 API 对齐。完整用法（创建应用与勾选权限、各类客户端的配置、容器内的 stdio 命令 ddp mcp）见仓库 docs/mcp.md。开关：默认关闭，在「系统设置 → MCP 服务」启用，未启用时返回 403；改完立即生效，不需要重启。鉴权二选一：Authorization: Basic base64(app_key:app_secret)，直接用「Open API」页面创建的应用凭据；或 Authorization: Bearer <令牌>，即 POST /api/open-api/token 换来的应用令牌或面板的登录令牌。注意 Basic 只有 MCP 接口认，其它开放 API 接口必须先换令牌再用 Bearer（见「开放 API → 获取开放 API Token」）。缺少凭据、凭据错误、应用已禁用、令牌无效或已过期都返回 401。能用哪些工具由应用勾选的权限范围决定，每次工具调用都记进「Open API」页面的调用日志，并计入应用的调用频率。协议是 Streamable HTTP 的无状态模式：请求体是 JSON-RPC 2.0，必须带 Content-Type: application/json 和 Accept: application/json, text/event-stream（缺前者回 415、缺后者回 400），每个 POST 独立处理、直接返回 JSON；GET / DELETE 返回 405。带 Origin 头的浏览器请求，来源必须与面板同源、在局域网 IP 下或在 config.yaml 的 cors.origins 里，否则返回 403。旧前缀 /api/mcp 同样可用。',
        auth: 'jwt',
        extraHeaders: { Accept: 'application/json, text/event-stream' },
        bodyParams: [
          { name: 'jsonrpc', type: 'string', required: true, description: '固定为 "2.0"', example: '2.0' },
          { name: 'id', type: 'integer', required: true, description: '请求 ID，响应里原样带回', example: '1' },
          { name: 'method', type: 'string', required: true, description: 'MCP 方法，常用 initialize / tools/list / tools/call。AI 客户端会自动完成这些调用，手工调试时直接发 tools/list 即可', example: 'tools/list' },
          { name: 'params', type: 'object', description: '方法参数：tools/list 传 {}；tools/call 见「调用工具与工具清单」', example: '{}' },
        ],
        helperExamples: {
          'curl 自测（Basic）': `curl -s ${getApiBaseOrigin()}/api/v1/mcp \\
  -u 'app_key:app_secret' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'`,
          '客户端配置（HTTP）': JSON.stringify({
            mcpServers: {
              'daidai-panel': {
                url: `${getApiBaseOrigin()}/api/v1/mcp`,
                headers: { Authorization: 'Basic <base64(app_key:app_secret)>' },
              },
            },
          }, null, 2),
          '客户端配置（stdio · Docker）': JSON.stringify({
            mcpServers: {
              'daidai-panel': {
                command: 'docker',
                args: ['exec', '-i', 'daidai-panel', 'ddp', 'mcp', '--app-key', '<app_key>', '--app-secret', '<app_secret>'],
              },
            },
          }, null, 2),
        },
        responseExample: JSON.stringify({
          jsonrpc: '2.0',
          id: 1,
          result: {
            tools: [
              {
                name: 'list_tasks',
                title: '查询任务列表',
                description: '分页查询定时任务，可按关键词、运行状态、分组、标签筛选。返回精简字段：enabled 是启用开关（与是否正在运行无关），status_text 是运行状态。',
                inputSchema: {
                  type: 'object',
                  properties: {
                    keyword: { type: 'string', description: '按任务名称或命令模糊搜索' },
                    group: { type: 'string', description: '按分组筛选（任务标签里的「分组:名称」，精确匹配名称）' },
                  },
                },
                annotations: { title: '查询任务列表', readOnlyHint: true, idempotentHint: true, openWorldHint: false },
              },
            ],
          },
        }, null, 2),
        responseFields: [
          { name: 'result.tools', type: 'array', description: '当前凭据可用的工具（name / title / description / inputSchema / annotations）。只开启 MCP 时是 16 个查询类工具，打开「允许写入与执行工具」后是 46 个；示例只列出一个、省略了部分参数' },
          { name: 'result.tools[].annotations', type: 'object', description: 'readOnlyHint=true 为查询类工具；写入类为 false，其中会改动或删除已有数据的还带 destructiveHint=true' },
          { name: '403', type: 'tip', description: 'MCP 服务未开启时返回 {"error":"MCP 服务未开启，请管理员在「系统设置 → MCP 服务」中启用"}；浏览器来源不在放行范围内也是 403' },
          { name: '401', type: 'tip', description: '缺少凭据、应用凭据无效、应用已被禁用、令牌无效 / 过期 / 已撤销，都返回 {"error":"…"}，不含任何工具信息' },
          { name: '405', type: 'tip', description: 'GET / DELETE 一律返回 405（带 Allow: POST）：服务是无状态的，不提供 SSE 长连接和会话' },
        ],
      },
      {
        id: 'mcp-tools',
        method: 'POST',
        path: '/api/v1/mcp',
        title: '调用工具与工具清单',
        description: '用 tools/call 调用工具：params.name 是工具名，params.arguments 是参数（每个工具的参数说明见 tools/list 返回的 inputSchema）。共 46 个工具，下表按「只读 / 写入」列出，并注明各自需要应用勾选的权限范围：16 个查询类工具开启 MCP 服务即可用；30 个写入与执行类工具还要在「系统设置 → MCP 服务」打开「允许写入与执行工具」，否则 tools/list 里看不到，直接调用也会失败；其中会删除或覆盖已有数据的带 destructiveHint=true。应用没勾选对应权限、参数不对或面板接口报错时，返回 result.isError=true，文本里带原因。单次输出上限约 64KB，超出会截断并注明，列表类工具可减小 page_size 或加关键词。read_script 按字节分段读取：传 offset / limit（默认且最多 49152 字节），返回 total_bytes、next_offset 与 truncated，truncated 为 true 时用 next_offset 继续读，各段拼起来就是完整文件。list_envs、export_envs 会遮蔽名称像凭据的变量值（规则与环境变量页一致），按关键词搜索时也不会按这些变量的值匹配。脚本类工具与 /scripts/* 接口一样，不能操作 .git 等版本库元数据目录、node_modules 与 __pycache__（返回「该路径不可访问」）。',
        auth: 'jwt',
        extraHeaders: { Accept: 'application/json, text/event-stream' },
        bodyParams: [
          { name: 'jsonrpc', type: 'string', required: true, description: '固定为 "2.0"', example: '2.0' },
          { name: 'id', type: 'integer', required: true, description: '请求 ID，响应里原样带回', example: '2' },
          { name: 'method', type: 'string', required: true, description: '固定为 tools/call', example: 'tools/call' },
          { name: 'params', type: 'object', required: true, description: '{"name": 工具名, "arguments": 参数对象}', example: '{"name":"list_tasks","arguments":{"status":"running","page_size":5}}' },
        ],
        responseExample: JSON.stringify({
          jsonrpc: '2.0',
          id: 2,
          result: {
            content: [{
              type: 'text',
              text: JSON.stringify({
                items: [{ command: 'task jd_sign.js', cron_expression: '0 9 * * *', enabled: false, group: '日常', id: 12, last_run_status: 0, last_run_status_text: '成功', name: '京东签到', status: 2, status_text: '运行中' }],
                page: 1,
                page_size: 5,
                total: 1,
              }),
            }],
          },
        }, null, 2),
        responseFields: [
          { name: 'result.content[0].text', type: 'string', description: '工具输出，统一是 JSON 文本；超过约 64KB 时截断并在末尾注明' },
          { name: 'result.isError', type: 'boolean', description: '工具执行失败时为 true（参数不对、应用缺少权限、面板接口报错等），text 里是原因' },
          { name: 'list_tasks', type: '只读', description: '权限 tasks。分页查询任务，可按关键词、运行状态、分组、标签筛选；enabled 是启用开关，status_text 是运行状态，env_scope 是可用环境变量范围（all / selected）' },
          { name: 'get_task', type: '只读', description: '权限 tasks。按 ID 查看任务完整配置' },
          { name: 'get_task_log', type: '只读', description: '权限 tasks。任务最近一次执行的日志，过长只保留末尾' },
          { name: 'list_task_labels', type: '只读', description: '权限 tasks。全部任务用过的自定义标签及各自的任务数，按名称升序；不含「分组:名称」与订阅这两类内部标签（分组用 list_tasks 的 group 筛选），标签名可传给 list_tasks 的 label 筛选任务（模糊匹配）' },
          { name: 'list_logs', type: '只读', description: '权限 logs。执行记录，可按任务、结果筛选，适合巡检失败任务' },
          { name: 'get_log', type: '只读', description: '权限 logs。按执行记录 ID 查看日志正文，过长只保留末尾' },
          { name: 'list_envs', type: '只读', description: '权限 envs。查询环境变量，名称像凭据的变量值会被遮蔽；输出带 important（用户标为「重要」的变量，面板不拦删除）' },
          { name: 'export_envs', type: '只读', description: '权限 envs。不分页导出环境变量（格式与 import_envs 一致），敏感变量同样遮蔽；输出带 important' },
          { name: 'list_scripts', type: '只读', description: '权限 scripts。脚本文件扁平列表' },
          { name: 'get_script_tree', type: '只读', description: '权限 scripts。脚本目录树，可按 path 只看子目录、按 max_depth 限制层数' },
          { name: 'read_script', type: '只读', description: '权限 scripts。按字节分段读取脚本内容（offset / limit），返回 total_bytes、next_offset、truncated' },
          { name: 'list_script_versions', type: '只读', description: '权限 scripts。脚本最近 50 个历史版本' },
          { name: 'list_subscriptions', type: '只读', description: '权限 subscriptions。查询订阅，含白名单、黑名单、依赖规则与最近拉取时间' },
          { name: 'list_backups', type: '只读', description: '权限 backup。备份目录里的备份文件' },
          { name: 'get_system_info', type: '只读', description: '权限 system。资源占用、面板版本、机器码、部署形态' },
          { name: 'get_dashboard', type: '只读', description: '权限 system。概览页统计' },
          { name: 'run_task', type: '写入', description: '权限 tasks。立即运行任务（禁用的任务也可以手动运行），结果用 get_task_log 查看' },
          { name: 'stop_task', type: '写入', description: '权限 tasks。停止运行中或排队中的任务' },
          { name: 'set_task_enabled', type: '写入', description: '权限 tasks。启用或禁用任务' },
          { name: 'batch_task_action', type: '写入', description: '权限 tasks。批量 enable / disable / run / stop / pin / unpin / delete，delete 不删脚本文件' },
          { name: 'batch_set_task_notify', type: '写入', description: '权限 tasks。批量开启 / 关闭失败、成功、终止通知，只改传入的开关；用 ids 指定任务，或 all=true 改全部任务；不改通知渠道（对应 PUT /api/tasks/batch/notify）' },
          { name: 'create_task', type: '写入', description: '权限 tasks。新建任务（名称、命令、定时规则、类型、超时、标签等），建好即启用' },
          { name: 'update_task', type: '写入', description: '权限 tasks。按 ID 修改任务，只改传入的字段；脚本改名后可用它同步命令；不改可用环境变量范围（只能在网页或 APP 里设置）' },
          { name: 'create_env', type: '写入', description: '权限 envs。新建环境变量' },
          { name: 'update_env', type: '写入', description: '权限 envs。按 ID 修改环境变量，只改传入的字段' },
          { name: 'delete_env', type: '写入', description: '权限 envs。按 ID 删除环境变量' },
          { name: 'batch_env_action', type: '写入', description: '权限 envs。批量 enable / disable / delete 环境变量' },
          { name: 'import_envs', type: '写入', description: '权限 envs。批量导入：merge（默认）覆盖名称与备注相同的变量；merge_value 把名称与值都相同的视为同一条、其余新增；replace 先删除全部现有变量再导入。条目可带 important：merge 只标不取消；replace 会连重要变量一起删除' },
          { name: 'save_script', type: '写入', description: '权限 scripts。新建或覆盖脚本，保留历史版本，可用 rollback_script 回滚' },
          { name: 'run_script', type: '写入', description: '权限 scripts。调试运行脚本并等待最多约 50 秒；仍在运行时返回 run_id，用它再调用可继续取输出，加 stop: true 可停止' },
          { name: 'run_code', type: '写入', description: '权限 scripts。不落盘直接执行代码片段（shell / bash / python / javascript / node / typescript / go），等待与续取方式同 run_script' },
          { name: 'delete_script', type: '写入', description: '权限 scripts。删除文件，或 type=directory 删除整个目录' },
          { name: 'batch_delete_scripts', type: '写入', description: '权限 scripts。一次删除最多 100 个文件或目录，返回失败项' },
          { name: 'rename_script / move_script / copy_script', type: '写入', description: '权限 scripts。重命名、移动、复制文件或目录，目标已有同名文件会被覆盖' },
          { name: 'rollback_script', type: '写入', description: '权限 scripts。把脚本恢复到某个历史版本' },
          { name: 'pull_subscription', type: '写入', description: '权限 subscriptions。立即拉取订阅，在后台进行' },
          { name: 'create_subscription / update_subscription', type: '写入', description: '权限 subscriptions。新建、修改订阅（修改只改传入的字段）' },
          { name: 'delete_subscription', type: '写入', description: '权限 subscriptions。删除订阅及其拉取记录，不删已同步的脚本与任务' },
          { name: 'set_subscription_enabled', type: '写入', description: '权限 subscriptions。启用或禁用订阅' },
          { name: 'send_notification', type: '写入', description: '权限 notifications。通过已配置的渠道发通知，可按渠道 ID / 名称 / 类型指定' },
          { name: 'create_backup / delete_backup', type: '写入', description: '权限 backup。创建（可只备份部分内容、可加密）或删除备份文件；删除只能用管理员账号的登录令牌连接，应用凭据调用会被拒绝' },
          { name: 'restore_backup', type: '写入', description: '用备份覆盖当前数据：备份包含的每一项都会先清空再写入，恢复配置还会替换开放 API 应用；恢复后请重启面板。只能用管理员账号的登录令牌连接，应用凭据即使有 backup 权限也会被拒绝' },
          { name: 'scripts / tasks 权限', type: 'tip', description: 'run_code、保存脚本再运行、新建或修改任务命令，都等于能在面板上执行任意代码；envs 权限能改写所有凭据；backup 权限能创建并下载包含全部数据的备份。请只授予完全信任的客户端' },
        ],
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
        description: '获取服务器系统信息（CPU/内存/磁盘/机器码等）。自 v3.0.4 起同层追加 deployment_type（docker / binary / magisk）与 magisk_shell_version（Magisk 模块外壳版本，非模块版为 0）；自 v3.3.0 起再追加 version（面板版本号），原有字段位置不变。多实例巡检时，拿一次本接口就能用 hostname + machine_code + version 对齐各实例的数据，不必再单独请求 /api/system/version。',
        auth: 'jwt',
        responseExample: JSON.stringify({
          data: { hostname: 'server', machine_code: 'A1B2C3D4-E5F6-4789-ABCD-1234567890AB', os: 'linux', arch: 'amd64', go_version: 'go1.22.0', num_cpu: 4, cpu_usage: 25.0, memory_total: 8589934592, memory_used: 4294967296, memory_usage: 50.0, disk_total: 107374182400, disk_used: 53687091200, disk_usage: 50.0, deployment_type: 'docker', magisk_shell_version: 0, version: '3.3.0' },
        }, null, 2),
        responseFields: [
          { name: 'data.machine_code', type: 'string', description: '面板机器码（大写 UUID v4），首次启动时生成并存进数据库，重启、升级都不会变，可作实例唯一标识。注意：用另一个实例的备份恢复「配置」后，机器码会变成那个实例的' },
          { name: 'data.version', type: 'string', description: '面板版本号（v3.3.0 起），与 /api/system/version 返回的 version 相同' },
          { name: 'data.hostname', type: 'string', description: '主机名。容器部署时通常是容器 ID，重建容器会变，不适合当实例标识' },
          { name: 'data.deployment_type', type: 'string', description: '部署形态：docker / binary / magisk' },
        ],
      },
      {
        id: 'system-machine-code',
        method: 'GET',
        path: '/api/system/machine-code',
        title: '获取面板机器码',
        description: '直接读取面板首次启动生成的机器码（UUID v4 大写格式）。不再需要手工查 SQL，OpenAPI 应用具备 system 权限即可调用。机器码重启、升级都不会变，可作实例唯一标识；/api/system/info 里也带着同一个 machine_code。',
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
        description: '提交一条命令并立即返回（201），实际输出用 run_id 轮询日志接口获取。权限：仅登录管理员（RequireAdmin），应用令牌（OpenAPI Token）不可用、一律 403，因为这等价于「在面板里跑任意系统命令」。边界：一次一条命令（可多行，会被整体喂给 bash -s），不是交互式终端 —— 没有 PTY，读标准输入的交互式命令（如未加 -y 的 apt）会直接读到 EOF 退出；每次执行都是独立进程，不保留上一条的 cd 与变量状态，工作目录固定为面板脚本目录。超时：由系统配置 console_timeout_minutes 决定（默认 30 分钟），到点终止整个进程组。后台进程：命令里用 & / nohup / setsid 起出来的孙进程会继承本次运行的输出管道。不自行重定向输出时，本次运行不会随命令本身立刻收尾，而是在命令本体退出约 2 秒后（服务端给命令行设了 WaitDelay）自行结束，按命令本体的真实退出码结算（bash 正常退出即 status=success），并在输出末尾追加一行「命令已结束；仍有后台进程持有输出管道，后续输出不再收集」，此后它的输出面板收不到了。⚠️ 注意此时本次运行已经结束：再调停止或清除记录都不会再动那个进程，它会留在容器里继续跑。只有命令仍在运行时调用停止、清除记录或撞上超时，杀的才是整个进程组。想干净收尾就自行重定向（setsid nohup xxx >/dev/null 2>&1 &）；真要留常驻进程也用 setsid 启动（定时 / 手动任务结束时会清理留在进程组里的后台进程，开机任务除外）。命令为空、超过 8192 字节、或系统里没有 bash 时返回 400。',
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
        description: '创建数据库与脚本数据备份；支持管理员账号 Token 或 OpenAPI 应用（需 backup 权限）调用。backup 权限的应用令牌只能创建、列出、下载备份与查看恢复进度；上传、恢复、删除备份必须用管理员账号本人的登录令牌，应用令牌调用一律 403。',
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
        description: '列出当前面板已生成的全部备份文件；支持管理员账号 Token 或 OpenAPI 应用（需 backup 权限）调用',
        auth: 'jwt',
        responseExample: JSON.stringify({ data: [{ filename: 'daidai-backup-20260101.json', size: 102400, modified_at: '2026-01-01T12:00:00Z' }] }, null, 2),
      },
      {
        id: 'backup-delete',
        method: 'DELETE',
        path: '/api/system/backup',
        title: '删除备份文件',
        description: '按文件名删除一个备份文件（filename 通过 query 传递）。权限：仅管理员账号本人的登录令牌（RequireAdmin），OpenAPI 应用令牌即使有 backup 权限也一律 403',
        auth: 'jwt',
        responseExample: JSON.stringify({ message: '删除成功' }, null, 2),
      },
    ],
  },
]

export function generateCodeExamples(endpoint: ApiEndpoint): Record<string, string> {
  const { method, path, bodyParams, auth } = endpoint
  // 额外请求头（如 MCP 要求的 Accept）按声明顺序跟在鉴权头后面；没声明时三段示例与原来逐字相同。
  const extraHeaderEntries = Object.entries(endpoint.extraHeaders ?? {})
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
      // 带点号的是某个对象参数的子键说明（如 editor.word_wrap、list.tasks_page_size），只给文档表格看；
      // 它们的取值已经体现在父参数（editor / list）的 example 里。按字面量写进示例的话，
      // 会多出 "editor.word_wrap" 这种服务端根本不认的顶层键，读者照抄还以为它们生效了。
      if (p.name.includes('.')) return
      if (p.example) {
        // number（如环境变量的 position 排序值）与 integer 一样按数字写进示例，否则会生成成字符串、发出去就 400
        if (p.type === 'integer' || p.type === 'number') {
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
        bodyObj[p.name] = p.type === 'integer' || p.type === 'number' ? 0 : p.type === 'boolean' ? false : p.type === 'array' ? [] : p.type === 'object' ? {} : ''
      }
    })
  }
  const bodyJson = JSON.stringify(bodyObj, null, 2)

  const authHeader = auth === 'jwt' ? `-H "Authorization: Bearer <TOKEN>"` : ''

  let shell = `curl -X ${method} "${url}"`
  if (authHeader) shell += ` \\\n  ${authHeader}`
  for (const [name, value] of extraHeaderEntries) shell += ` \\\n  -H "${name}: ${value}"`
  if (hasBody) shell += ` \\\n  -H "Content-Type: application/json" \\\n  -d '${bodyJson}'`

  let js = `const res = await fetch("${url}", {\n  method: "${method}",\n  headers: {\n    "Content-Type": "application/json",`
  if (auth === 'jwt') js += `\n    "Authorization": "Bearer <TOKEN>",`
  for (const [name, value] of extraHeaderEntries) js += `\n    "${name}": "${value}",`
  js += `\n  },`
  if (hasBody) js += `\n  body: JSON.stringify(${bodyJson}),`
  js += `\n})\nconst data = await res.json()\nconsole.log(data)`

  const pythonHeaders: string[] = []
  if (auth === 'jwt') pythonHeaders.push(`"Authorization": "Bearer <TOKEN>"`)
  for (const [name, value] of extraHeaderEntries) pythonHeaders.push(`"${name}": "${value}"`)
  const pythonHeadersArg = pythonHeaders.length ? ', headers=headers' : ''

  let python = `import requests\n\n`
  if (pythonHeaders.length) python += `headers = {${pythonHeaders.join(', ')}}\n`
  if (hasBody) {
    python += `data = ${bodyJson}\n`
    python += `res = requests.${method.toLowerCase()}("${url}"${pythonHeadersArg}, json=data)\n`
  } else {
    python += `res = requests.${method.toLowerCase()}("${url}"${pythonHeadersArg})\n`
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
