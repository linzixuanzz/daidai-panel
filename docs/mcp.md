# 内置 MCP 服务

呆呆面板内置了一个 [MCP（Model Context Protocol）](https://modelcontextprotocol.io/) 服务，Claude Desktop、Cursor、Cherry Studio、AstrBot 等支持 MCP 的 AI 客户端连上之后，就能直接用自然语言查任务、看日志、巡检失败任务；管理员放开写入权限后，还可以让 AI 新建和修改任务、改环境变量、管理脚本文件、执行代码片段、管理订阅、发通知、备份与恢复。工具集与开放 API 的能力对齐：开放 API 能做的日常运维，MCP 基本都有对应的工具。

提供两种连接方式：

| 方式 | 地址 / 命令 | 适合 |
|---|---|---|
| HTTP（Streamable HTTP） | `POST http://<面板地址>/api/v1/mcp` | 大多数客户端；手机（Magisk 模块版）只能用这个 |
| stdio | 容器内的 `ddp mcp` 命令 | 只支持「拉起本地进程」的客户端，例如 Claude Desktop |

两种方式的工具完全一样：所有工具都经面板的开放 API 执行，**能访问哪些模块由应用的权限范围决定**，每一次工具调用都会记进「Open API」页面的调用日志，并计入应用的调用频率限制。

---

## 1. 开启

MCP 默认关闭。在面板 **系统设置 → MCP 服务** 里：

- **启用 MCP 服务**：打开后才能连接，否则 HTTP 接口一律返回 403，`ddp mcp` 直接报错退出。
- **允许写入与执行工具**：默认关闭，此时 AI 只能用查询类工具（看不到、也调不到写入类工具）。打开后才会提供运行任务、修改环境变量、保存与运行脚本等工具。

两个开关改完立即生效，不需要重启面板。已经连着的 `ddp mcp` 进程在每次调用工具前都会重新检查开关：关掉之后再调用会直接拿到错误。

## 2. 创建应用并勾选权限

在 **Open API** 页面新建一个应用，记下 `app_key` 与 `app_secret`（密钥只显示一次），再按需要勾选权限范围：

| 权限范围 | 对应的工具 |
|---|---|
| `tasks` | list_tasks、get_task、get_task_log、list_task_labels、run_task、stop_task、set_task_enabled、batch_task_action、create_task、update_task、batch_set_task_notify |
| `logs` | list_logs、get_log |
| `envs` | list_envs、export_envs、create_env、update_env、delete_env、batch_env_action、import_envs |
| `scripts` | list_scripts、get_script_tree、read_script、list_script_versions、save_script、run_script、run_code、delete_script、rename_script、move_script、copy_script、batch_delete_scripts、rollback_script |
| `subscriptions` | list_subscriptions、pull_subscription、create_subscription、update_subscription、delete_subscription、set_subscription_enabled |
| `system` | get_system_info、get_dashboard |
| `notifications` | send_notification |
| `backup` | list_backups、create_backup、delete_backup、restore_backup |

没有勾选的模块，对应工具调用时会返回「应用无权访问此资源」。建议只勾选真正需要的范围。用登录令牌连接时按账号角色限制，其中备份类工具要求管理员。`delete_backup`、`restore_backup` 会销毁或覆盖数据，只允许管理员账号本人：用应用凭据连接时即使勾了 `backup` 也会被拒绝。

> ⚠️ `scripts` 权限等于可以在面板上执行任意代码（`run_code` 直接执行代码片段，或保存脚本再运行）；`tasks` 权限可以新建、修改任务的命令，同样等于任意代码执行；`backup` 权限能创建并下载包含全部数据（含环境变量、密钥）的备份。请只授予完全信任的 AI 客户端。

## 3. HTTP 方式

地址与浏览器访问面板的地址相同，后面加 `/api/v1/mcp`，例如 `http://192.168.1.10:5700/api/v1/mcp`。

鉴权二选一：

- `Authorization: Basic <base64(app_key:app_secret)>`：直接用应用凭据，最省事；
- `Authorization: Bearer <令牌>`：用 `POST /api/v1/open-api/token` 换来的应用令牌（24 小时有效），或者面板的登录令牌。

> **MCP 与开放 API（REST）的鉴权不一样，别混用：**
>
> | 入口 | 鉴权方式 |
> |---|---|
> | MCP（`/api/v1/mcp`） | `Basic base64(app_key:app_secret)` 直接用凭据，也接受 Bearer 令牌 |
> | 开放 API（其余 `/api/v1/...` 接口） | 只认 Bearer：先 `POST /api/v1/open-api/token`（请求体 `{"app_key": "…", "app_secret": "…"}`）换取 `access_token`（`expires_in` 为 86400 秒），再带 `Authorization: Bearer <access_token>` |
>
> REST 接口不接受 Basic，也不能把 `app_secret` 直接当 Bearer 令牌用：后者返回 `401 {"error":"令牌无效或已过期"}`；带 Basic 或完全没带 Authorization 时返回 `401 {"error":"缺少授权令牌"}`。两种都说明要先去换令牌。

生成 Basic 凭据：

```bash
# Linux / macOS
echo -n 'app_key:app_secret' | base64
```

```powershell
# Windows PowerShell
[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes('app_key:app_secret'))
```

支持远程 MCP 地址的客户端（例如 Cursor），配置示例：

```json
{
  "mcpServers": {
    "daidai-panel": {
      "url": "http://192.168.1.10:5700/api/v1/mcp",
      "headers": {
        "Authorization": "Basic <上面生成的 base64>"
      }
    }
  }
}
```

只支持 stdio 的客户端，可以借助 [`mcp-remote`](https://www.npmjs.com/package/mcp-remote) 转一层（需要本机有 Node.js）：

```json
{
  "mcpServers": {
    "daidai-panel": {
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "http://192.168.1.10:5700/api/v1/mcp",
        "--header", "Authorization:Basic <上面生成的 base64>"
      ]
    }
  }
}
```

用 curl 自测连通性（应当返回工具清单）：

```bash
curl -s http://192.168.1.10:5700/api/v1/mcp \
  -u 'app_key:app_secret' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

说明：

- 服务是无状态的：每个 POST 独立处理、直接返回 JSON，不开 SSE 长连接，`GET` / `DELETE` 返回 405。
- 浏览器来源（带 `Origin` 头）的请求，要求来源与面板同源、在局域网 IP 下，或在 `config.yaml` 的 `cors.origins` 里，否则返回 403。
- 公网访问时请务必套 HTTPS：Basic 凭据与令牌在明文 HTTP 上会被同一网络里的人看到。

## 4. stdio 方式（`ddp mcp`）

`ddp mcp` 在面板所在的机器 / 容器里运行，经本机 HTTP 调用正在运行的面板（面板必须在运行），自己不改数据库。

```text
ddp mcp --app-key <app_key> --app-secret <app_secret> [--url http://127.0.0.1:5701]
```

| 参数 | 环境变量 | 说明 |
|---|---|---|
| `--url` | `DDP_MCP_URL` | 面板后端地址，默认 `http://127.0.0.1:<面板后端端口>` |
| `--app-key` | `DDP_MCP_APP_KEY` | 应用的 app_key |
| `--app-secret` | `DDP_MCP_APP_SECRET` | 应用的 app_secret |

stdout 只输出 MCP 协议消息，就绪提示与错误都写到 stderr。

**Docker 部署**（客户端在宿主机上）：

```json
{
  "mcpServers": {
    "daidai-panel": {
      "command": "docker",
      "args": [
        "exec", "-i", "daidai-panel",
        "ddp", "mcp", "--app-key", "<app_key>", "--app-secret", "<app_secret>"
      ]
    }
  }
}
```

`daidai-panel` 换成你的容器名；`-i` 不能少，否则 stdin 接不上。

**二进制部署**：`command` 直接写 ddp 的完整路径，`args` 为 `["mcp", "--app-key", "…", "--app-secret", "…"]`。

**Magisk 模块版**：面板跑在手机的容器里，电脑上的客户端进不去这个容器，请改用上面的 HTTP 方式。

## 5. 工具清单

查询类（开启 MCP 即可用）：

| 工具 | 说明 |
|---|---|
| `list_tasks` | 分页查询任务，可按关键词、运行状态、分组、标签筛选；`enabled` 是启用开关，`status_text` 是运行状态，`env_scope` 是可用环境变量范围（all / selected） |
| `get_task` | 按 ID 查看任务完整配置 |
| `get_task_log` | 任务最近一次执行的日志（过长只保留末尾） |
| `list_task_labels` | 全部任务用过的自定义标签及各自的任务数，按名称升序；不含「分组:名称」与订阅这两类内部标签（分组用 `list_tasks` 的 `group` 筛选）；标签名可传给 `list_tasks` 的 `label` 筛选任务（模糊匹配） |
| `list_logs` | 执行记录，可按任务、结果筛选，适合巡检失败任务 |
| `get_log` | 按执行记录 ID 查看日志正文（过长只保留末尾） |
| `list_envs` | 查询环境变量，名称像凭据的变量值会被遮蔽；每条带 `important`（用户标为「重要」的变量，删除或替换导入前请先向用户确认，面板本身不拦删除） |
| `export_envs` | 不分页导出环境变量（格式与 `import_envs` 一致），敏感变量同样遮蔽；输出带 `important`；可用 `ids` 分批 |
| `list_scripts` | 脚本文件扁平列表（可按路径关键词过滤）；`tree: true` 返回面板原始目录树，更推荐用 `get_script_tree` |
| `get_script_tree` | 脚本目录树（含空目录）；`path` 只看某个子目录，`max_depth` 限制展开层数 |
| `read_script` | 按字节分段读取脚本内容，见下方「分段读取」 |
| `list_script_versions` | 某个脚本最近 50 个历史版本，`id` 用于 `rollback_script` |
| `list_subscriptions` | 查询订阅 |
| `list_backups` | 备份目录里的备份文件 |
| `get_system_info` | 资源占用、面板版本 `version`、机器码 `machine_code`、部署形态。`machine_code` 在面板首次启动时生成，重启、升级都不会变，多实例巡检时可作实例唯一标识（注意：用另一个实例的备份恢复「配置」后，机器码会变成那个实例的） |
| `get_dashboard` | 概览页统计 |

写入与执行类（还需要开启「允许写入与执行工具」）。标 ⚠️ 的会删除或覆盖已有数据（工具注解带 `destructiveHint: true`），不可撤销：

| 工具 | 说明 |
|---|---|
| `run_task` / `stop_task` | 运行 / 停止任务 |
| `set_task_enabled` | 启用或禁用任务 |
| `batch_task_action` ⚠️ | 批量 enable / disable / run / stop / pin / unpin / delete（delete 不删脚本文件） |
| `create_task` | 新建任务（名称、命令、定时规则、类型、超时、标签、重试、通知等），建好即启用 |
| `update_task` ⚠️ | 按 ID 修改任务，只改传入的字段；脚本改名 / 移动后用它同步任务命令；不改可用环境变量范围（只能在网页或 APP 里设置） |
| `batch_set_task_notify` ⚠️ | 批量打开 / 关闭失败、成功、终止通知，只改传入的开关、不改通知渠道；`ids` 指定任务，或 `all: true` 改全部任务；已在排队的那一次执行仍用旧设置 |
| `create_env` / `update_env` ⚠️ / `delete_env` ⚠️ | 新建、修改、删除环境变量 |
| `batch_env_action` ⚠️ | 批量 enable / disable / delete 环境变量 |
| `import_envs` ⚠️ | 批量导入：`merge`（默认）覆盖「名称 + 备注」相同的变量、其余新增；`merge_value` 把「名称 + 值」相同的视为同一条、更新其备注（给了才写）/ 分组 / 启用状态，其余新增；`replace` 先删除全部现有变量再导入。导入前先校验变量名，并拒绝遮蔽后的值。条目可带 `important`：`merge` 只会把已有变量标为重要、不会取消标记；`replace` 会连重要变量一起删除 |
| `save_script` ⚠️ | 新建或覆盖脚本（保留历史版本，可用 `rollback_script` 回滚） |
| `run_script` | 调试运行脚本并等待最多约 50 秒；仍在运行时返回 `run_id`，用同一个 `run_id` 再调用可继续取输出，加 `stop: true` 可停止 |
| `run_code` | 直接执行一段代码（不落盘），`language` 可选 `shell`（或 `bash`）、`python`、`javascript`、`node`、`typescript`、`go`；工作目录是临时目录，脚本目录的绝对路径在环境变量 `DAIDAI_SCRIPTS_DIR` 里；等待与续取方式同 `run_script` |
| `delete_script` ⚠️ | 删除文件，或 `type: directory` 删除整个目录 |
| `batch_delete_scripts` ⚠️ | 一次删除最多 100 个文件 / 目录，逐项执行，返回失败项 |
| `rename_script` ⚠️ / `move_script` ⚠️ / `copy_script` ⚠️ | 重命名、移动、复制文件或目录；目标位置已有同名文件会被覆盖，引用它的任务命令不会自动更新 |
| `rollback_script` ⚠️ | 把脚本恢复到某个历史版本（回滚本身也记一个新版本） |
| `pull_subscription` | 立即拉取订阅（后台进行） |
| `create_subscription` | 新建订阅（Git 仓库或单文件） |
| `update_subscription` ⚠️ / `delete_subscription` ⚠️ | 修改、删除订阅（删除不删已同步的脚本和任务） |
| `set_subscription_enabled` | 启用或禁用订阅 |
| `send_notification` | 通过已配置的通知渠道发消息，可按渠道 ID / 名称 / 类型指定，不指定则发给全部「默认推送」渠道 |
| `create_backup` ⚠️ | 创建备份（可只备份其中几项、可加密）；与已有备份同名时覆盖 |
| `delete_backup` ⚠️ | 删除备份文件；只能用管理员账号的登录令牌连接，应用凭据调用会被拒绝 |
| `restore_backup` ⚠️ | 用备份覆盖当前数据：备份里包含的每一项都会先被清空再写入，恢复配置还会替换开放 API 应用（当前凭据可能失效）；网页端恢复后会重启面板，这个工具不会，恢复后请重启一次面板。只能用管理员账号的登录令牌连接，应用凭据调用会被拒绝 |

单次工具输出上限约 64KB，超出部分会截断并注明；列表类工具可以减小 `page_size` 或加关键词缩小范围。

### 分段读取（`read_script`）

大文件按字节分段读，不会再静默截断：

- 参数：`offset` 起始字节（默认 0），`limit` 本段最多字节数（默认且最多 49152）。
- 返回：`content`、`offset`（本段实际起点）、`next_offset`（下一段起点）、`total_bytes`（文件总字节数）、`truncated`（是否还有没读的内容）。
- `truncated` 为 `true` 时，把 `next_offset` 作为下一次的 `offset` 继续读，直到 `truncated` 为 `false`（此时 `next_offset` 等于 `total_bytes`）。各段按顺序拼起来就是完整文件。
- 分段一定落在 UTF-8 字符边界上：传入的 `offset` 落在多字节字符中间时，会退回到这个字符的开头（返回的 `offset` 就是实际起点）。内容里引号、换行这类需要转义的字符很多时，单段会比 `limit` 短一些，以免整条输出超过 64KB。

### 不能通过脚本接口操作的目录

脚本相关的工具（以及开放 API 的 `/scripts/*` 接口）对下面这些目录一律返回「该路径不可访问」，路径里任意一段命中都算，不区分大小写：

| 目录 | 原因 |
|---|---|
| `.git`、`.svn`、`.hg`、`.bzr` | 版本库元数据。订阅用 Token 鉴权时，仓库的 `.git/config` 里存着带访问令牌的地址，能读就等于把令牌交给任何有脚本权限的人，所以写死不可配置 |
| `node_modules`、`__pycache__`（以及启动时被自动隔离的异常目录 `%systemdrive%`） | 依赖与字节码缓存，不属于脚本本身，在脚本列表、统计和备份里也都被忽略 |

这是有意的安全限制，不是临时故障，重试不会成功。确实要清理这些目录（例如删掉 `__pycache__`），可以用 `run_code` 执行 `rm -rf "$DAIDAI_SCRIPTS_DIR/某个子目录/__pycache__"`，或在面板的终端里操作。

## 6. 安全提示

- 默认关闭，开启后也默认只读；写入与执行需要管理员再单独打开。
- 权限完全沿用开放 API：应用只能碰勾选了的模块，禁用应用立即失效；登录令牌则按账号角色限制。
- 环境变量的遮蔽规则与网页端一致（名称含 TOKEN、SECRET、PASSWORD、COOKIE 等，或某一段是 KEY、PWD、PASS、AUTH、CK 等），只作用于 MCP 的输出（`list_envs`、`export_envs` 以及增删改的返回值），目的是不把凭据送进 AI 的上下文；`list_envs` 的关键词搜索也不会按这些变量的值匹配，免得被拿来逐字试出凭据。`import_envs` 会拒绝把遮蔽后的值（如 `pt_******zz;`）导回敏感变量，免得真实凭据被星号覆盖。拥有 `envs` 权限的应用直接调开放 API 仍能读到明文。
- 每次工具调用都会记进「Open API」页面的调用日志，并计入应用的调用频率限制；`run_script` / `run_code` 等待期间的轮询也算在内。
- `scripts` 与 `tasks` 权限都等于任意代码执行，`envs` 权限能改写所有凭据，`backup` 权限能创建并下载包含全部数据的备份，请按需授予。`restore_backup` 前务必先 `create_backup` 留一份。
