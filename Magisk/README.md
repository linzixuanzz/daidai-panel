# 呆呆面板 Magisk 模块

通过 Magisk / KernelSU / APatch 在已 Root 的 Android 设备上运行呆呆面板。开机自启，浏览器访问 `http://127.0.0.1:5700` 即可使用；后端绑定 `0.0.0.0`，局域网 / 内网穿透也能直连。

> 本模块无需 Docker、无需 Termux。安装阶段会下载一份 Alpine 3.18 minirootfs 到 `/data/daidai`，在容器里 `apk` 装好 Python / Node.js / git 等运行时后，把 `daidai-server` 放进容器启动。运行期等同于"用 root 起了一个极小号 Linux 容器跑面板"。

---

## 架构与目录（必读）

和 Docker 版的 Nginx + Go 两层结构不同，Magisk 版只有一层：前端静态资源由 `daidai-server` 在单端口（默认 `5700`）上直接托管。整个运行时放进 Alpine 容器（通过仓库自带的 `rurima` 进入），数据 / 日志 / 脚本都落在容器内路径。

```
/data/adb/modules/daidai-panel/        ← 模块本体（随模块卸载清除）
├── module.prop
├── customize.sh / service.sh / uninstall.sh / action.sh
├── scripts/check-runtimes.sh
└── system/bin/
    ├── rurima                         ← 容器运行时（静态二进制）
    ├── daidai-server                  ← 后端（每次开机同步进容器）
    └── ddp                            ← CLI（同上）

/data/daidai/（或 /data/local/daidai/）← Alpine minirootfs 容器，占 300MB+
├── usr/local/bin/daidai-server        ← 实际在跑的后端
├── usr/local/bin/ddp                  ← 容器内 CLI
├── app/web/                           ← 前端静态
└── app/Dumb-Panel/                    ← 所有用户数据
    ├── config.yaml                    ← 每次启动由 service.sh 重新生成
    ├── daidai.db                      ← SQLite 数据库
    ├── daidai.log / service.log       ← 后端 / 容器启动脚本日志
    └── scripts/ logs/ backups/ deps/

/data/adb/daidai-panel/                ← 宿主侧持久化目录（不随模块升级清除）
├── ports.conf                         ← 端口配置，唯一可手动改的地方
├── service.log                        ← 宿主侧启动日志
└── module.prop                        ← 版本号兜底（升级对比用）
```

## 系统要求

- 已 Root 的 Android 设备，至少满足以下任意 root 方案之一：
  - Magisk **v24.0+**
  - KernelSU
  - APatch
- Android 8.0 (API 26) 及以上
- CPU 架构：`arm64`（aarch64）或 `x86_64`，CI 两种架构都会发布
- **剩余可用空间 ≥ 1.5 GB**（Alpine rootfs ~300 MB + 依赖 + 数据 / 日志）
- **安装阶段需要联网**（下载 Alpine minirootfs + apk 联网装 python3 / nodejs / git 等）

## 安装

1. 下载 `daidai-panel-magisk-vX.Y.Z.zip`（或按[下面章节](#本地构建)自行构建）。
2. 打开 Magisk / KernelSU / APatch 管理器 →「模块」→「从本地安装」，选该 ZIP。
3. 等几分钟，Alpine 下载 + apk 装依赖完成后会出现 "安装完成！" 提示。
4. 重启手机。
5. 手机浏览器访问 `http://127.0.0.1:5700`，按提示初始化管理员账号。

## 在模块卡片内一键更新

模块 `module.prop` 里已经填好了 `updateJson`：

```
updateJson=https://github.com/linzixuanzz/daidai-panel/releases/latest/download/update.json
```

这是 **GitHub Release 的稳定跳转地址**，会自动指向"当前最新一次 Release"里随附的 `update.json`。因此：

1. 每次仓库推送新的 `vX.Y.Z` tag，工作流会自动：
   - 编译 arm64 + amd64 静态后端
   - 打包 `daidai-panel-magisk-vX.Y.Z.zip`
   - 生成指向本次 Release 的 `update.json`（含版本号 / versionCode / zipUrl / changelog）
   - 把这两个文件一起上传到 Release
2. 已装旧版本的手机，打开管理器时自动拉取 `update.json`，比 `versionCode` 发现有新版 → 模块卡片出现「**更新**」按钮
3. 点按钮 → 管理器自动下载 ZIP 并走安装流程（等同手动「从本地安装 ZIP」）
4. 重启手机完成升级。升级流程内部：`customize.sh` 先把容器里的 `/app/Dumb-Panel/` 整个备份到 `TMPDIR/backup_data`，然后清掉旧 rootfs 重装 Alpine，装完再把备份复原回去——数据库、脚本、日志、依赖全部保留

> 说明：需要管理器版本支持 `updateJson`（Magisk v24.0+、KernelSU、APatch 新版均支持）。如果你自己 fork 了本项目发版，请把 `module.prop` 里的 `linzixuanzz/daidai-panel` 替换成自己的仓库路径即可。

### 手动触发更新检查

部分管理器默认只在打开模块列表时刷新一次。想立即触发，可以：

- **Magisk**：在「模块」页面下拉刷新
- **KernelSU / APatch**：在「模块」页面点右上角的刷新图标

如果希望强制下载最新 ZIP（比如想跳过 versionCode 比较），也可以直接从 Release 页下载 ZIP 手动安装，数据目录同样不会被清。

---

## 脚本运行时

`customize.sh` 会在安装阶段进容器执行一遍 `apk add`，装好定时任务通常需要的全套运行时：

- **基础**：`bash`、`coreutils`、`build-base`（gcc / make / pkgconfig 等）
- **脚本解释器**：`python3` + `python3-dev` + `py3-pip`、`nodejs` + `npm`（默认镜像源已切到 `npmmirror.com`）
- **工具链**：`git`、`curl`、`wget`、`jq`、`openssh`、`openssl`、`tzdata`、`procps`、`netcat-openbsd`
- 离线打包的 `linux-pam` / `shadow`，保证 `sshd` 和用户管理可用

面板「依赖管理」页的 `pip` / `npm` 直接可用；定时任务跑 Python / Node.js / Shell / Git 脚本无需额外配置，**不需要 Termux，也不需要自备静态二进制**。

> 容器是 Alpine musl 基础。遇到只有 glibc 预编译包（例如某些商用脚本自带的 `.whl`）时，请改用源码安装或找 musl wheel。

---

## 在管理器内查看状态（推荐）

模块内置 `action.sh`。在 **Magisk v26+ / KernelSU / APatch** 的模块列表里，呆呆面板条目右侧会出现「运行 / Action」按钮，点击会直接在管理器弹窗里打印：

- 当前端口配置（`PANEL_PORT` / `SSH_PORT` / `EXTRA_CORS_ORIGINS`）
- 面板进程状态 + 容器内 PID
- 宿主侧 `PANEL_PORT` 的实际监听情况
- 容器运行时自检（`python3` / `node` / `npm` / `git` / `curl` / `bash` 的路径与版本）
- `service.log`（宿主侧启动日志）最近 60 行
- `daidai.log`（容器内后端日志）最近 60 行

排障的第一步永远是先点这个按钮看输出，不用 adb 连线。

## 常用操作

```bash
# 宿主侧 —— 启动日志
su -c "tail -f /data/adb/daidai-panel/service.log"

# 进入 Alpine 容器（获得完整 bash / apk / python / node / git / ddp）
MODDIR=/data/adb/modules/daidai-panel
ROOTFS=/data/daidai                   # 少数设备在 /data/local/daidai
su -c "$MODDIR/system/bin/rurima ruri -p -N -S -A $ROOTFS /bin/bash"
```

进到容器里之后，`ddp` 就是正常命令。所有运维 / 备份 / 账号操作都在容器里执行：

```bash
ddp status
ddp list-users
ddp reset-password admin NewPass123
ddp backup create --name nightly
ddp backup list
```

> **`ddp` 必须在容器内执行**——数据库路径 `/app/Dumb-Panel/daidai.db` 只在容器内有意义，直接用宿主侧 `/data/adb/modules/daidai-panel/system/bin/ddp` 会找不到数据库。

**不重启手机、只重启面板**（改端口 / 换二进制后让配置立即生效）：

```bash
su -c "pkill -f daidai-server; sh /data/adb/modules/daidai-panel/service.sh"
```

> `service.sh` 检测到 `daidai-server` 已在跑时会直接跳过（避免重复拉起），所以 **必须先 `pkill`** 才能让新配置生效，单独再跑一次 `service.sh` 是无效的。

## 忘记密码

进容器后用 `ddp` 操作，**无需卸载 / 重装 / 删库**：

```bash
# 进容器
su -c "/data/adb/modules/daidai-panel/system/bin/rurima ruri -p -N -S -A /data/daidai /bin/bash"

# 容器内
ddp list-users                              # 忘了用户名先看这个
ddp reset-password <用户名> <新密码>
ddp disable-2fa <用户名>                    # 绑定了 2FA 但 TOTP 也进不去
ddp reset-login --all                       # 登录失败次数过多被锁
```

## 修改端口

模块版没有 Docker 那套 nginx 反代，前端 / 后端都由同一个 `daidai-server` 二进制在 `PANEL_PORT` 上直接托管，绑定 `0.0.0.0`，本机 / 局域网 / 内网穿透都能直连。

> **不要手动改 `/data/adb/daidai-panel/config.yaml`**——每次开机 `service.sh` 都会按 `ports.conf` 重新生成 config.yaml，手动改的内容会被覆盖掉。

**端口配置的唯一入口**是 `/data/adb/daidai-panel/ports.conf`（首次安装模块时自动生成，内含注释）：

```bash
su
vi /data/adb/daidai-panel/ports.conf
```

支持 3 个可选变量：

| 变量 | 作用 | 默认 |
|------|------|------|
| `PANEL_PORT` | 浏览器访问面板的端口 | `5700` |
| `SSH_PORT` | 容器内 SSH 端口（adb / Termux 登入容器调试用） | `22` |
| `EXTRA_CORS_ORIGINS` | 额外 CORS 白名单，英文逗号分隔；仅在跨域场景需要（内网穿透公网端口与面板端口不同，或用域名访问） | 空 |

示例：

```ini
PANEL_PORT=6700
SSH_PORT=2222
EXTRA_CORS_ORIGINS="https://panel.example.com,https://xx.trycloudflare.com"
```

> `service.sh` 启动时会自动校验端口合法性（必须是 1-65535 的整数），非法值会回退到默认并写入 `service.log`。

**生效方式**（任选其一）：

```bash
# 方式 1：重启手机，service.sh 开机自动重跑
# 方式 2：先 kill 旧 daidai-server 再重跑 service.sh（不用重启手机）
su -c "pkill -f daidai-server; sh /data/adb/modules/daidai-panel/service.sh"
```

> 单独再跑一次 `service.sh` 是**无效的**——它检测到 `daidai-server` 已在跑会直接跳过（避免重复拉起），所以必须先 `pkill` 让旧进程退出，新进程才会按新 `ports.conf` 重新生成 `config.yaml` 并绑定新端口。

改完后想确认实际监听状态，可以在 Magisk / KernelSU / APatch 管理器里点模块卡片的「运行」按钮，会直接打印 `PANEL_PORT` / `SSH_PORT` 的当前监听情况。

## 对系统的影响

本模块是**纯用户态 / 非侵入式**的：

| 类别 | 是否触碰 | 说明 |
|------|----------|------|
| `/system` 分区 | ❌ | 不修改系统文件，纯 Magisk 魔挂 |
| `system.prop` / `sepolicy.rule` | ❌ | 不写系统属性、不加 SELinux 规则 |
| 应用安装 / 广告 / 服务伪装 | ❌ | 不装 APK、不注册账户、不开后台伪装 |
| 网络监听 | ⚠️ | 绑定 `0.0.0.0:PANEL_PORT`（默认 5700）+ 容器内 `sshd` 监听 `0.0.0.0:SSH_PORT`（默认 22），局域网任何人都能尝试连接 |
| 写入位置 | ✅ | 三处：`/data/adb/modules/daidai-panel/`（模块本体）、`/data/daidai/` 或 `/data/local/daidai/`（Alpine 容器 + 所有用户数据，占大空间）、`/data/adb/daidai-panel/`（端口配置 + 启动日志） |

> **局域网可见性**：面板后端默认对局域网开放。家里 / 自己 WiFi 没问题；公共网络（咖啡馆、公司 Guest Wi-Fi）建议把 `SSH_PORT` 换掉或进容器 `rc-service sshd stop`，并在路由器 / 防火墙层面限制面板端口。

> **禁用 ≠ 停服**：在管理器里「禁用」模块只阻止下次开机加载，**不会 kill 当前的容器进程**（`daidai-server` 是 `rurima` 启的独立进程树）。想立即停：`su -c "pkill -f daidai-server"`。

## 卸载（默认彻底清理，不留痕迹）

1. 在 Magisk / KernelSU / APatch 管理器中移除本模块
2. 重启手机

重启完成后 `uninstall.sh` 会自动做：

- `TERM` + `KILL` 掉仍在运行的 `daidai-server` 进程
- 删除 Alpine rootfs `/data/daidai` 和 `/data/local/daidai`（数百 MB，**面板所有数据都在这里**）
- 删除宿主侧持久化目录 `/data/adb/daidai-panel/`（端口配置 + 启动日志）
- 清掉历史版本可能留下的 `init.d` / `service.d` 脚本

模块本体 `/data/adb/modules/daidai-panel/` 由 Magisk / KernelSU / APatch 框架负责清除。重启完成后设备上不会残留任何呆呆面板相关文件。

### 想保留数据以便日后重装？

在卸载前打一个保留标记，`uninstall.sh` 看到它就会跳过 rootfs 和持久化目录清理：

```bash
su -c "touch /data/adb/daidai-panel/.keep_on_uninstall"
```

标记文件本体会随 rootfs 保留下来（就在 `/data/adb/daidai-panel/` 里）。后续想彻底删：

```bash
su -c "rm -rf /data/daidai /data/local/daidai /data/adb/daidai-panel"
```

### 想卸载前先导出一份备份？

先进容器执行 `ddp backup`：

```bash
# 进容器
su -c "/data/adb/modules/daidai-panel/system/bin/rurima ruri -p -N -S -A /data/daidai /bin/bash"

# 容器内
ddp backup create --name before-uninstall
```

备份落在容器的 `/app/Dumb-Panel/backups/`，对应宿主侧路径是 `/data/daidai/app/Dumb-Panel/backups/`（KernelSU 用 `/data/local/daidai/...`）。先 `adb pull` 或者 MT 管理器拷到电脑，然后再卸载即可。

## 本地构建

在项目根目录执行：

```bash
# 默认只打 arm64
bash Magisk/build.sh 2.0.6

# 只打 amd64
bash Magisk/build.sh 2.0.6 amd64

# 同时打 arm64 + amd64
bash Magisk/build.sh 2.0.6 all
```

构建产物：`dist/daidai-panel-magisk-v<版本>.zip`（`module.prop` 里的 `version` / `versionCode` 会自动按参数同步）。

前置依赖：

- **Go 1.22+**（静态编译 `CGO_ENABLED=0`）
- **Node.js 20+**（首次构建自动跑 `npm ci && npm run build`，已有 `web/dist` 会跳过）
- `zip` 或 `python3`（Windows Git Bash 下没有 `zip` 时会 fallback 到 Python 打包）
- 仓库自带的 `Magisk/system/bin/rurima`（~720 KB 静态二进制，打包时会拷进 ZIP）

## FAQ

**Q: 安装时卡在"正在联网下载 Alpine rootfs" / "正在联网安装面板运行依赖"**

这两步强依赖网络：Alpine rootfs ~3 MB、apk 装依赖累计约 50 MB。`customize.sh` 默认用 NJU 镜像 `mirrors.nju.edu.cn`。公司 / 学校网络被墙的话挂 VPN 重装即可。超时失败模块会自动 abort，不会损坏已有数据。

**Q: 浏览器打不开 `http://127.0.0.1:5700`**

1. 先点模块卡片「运行」按钮，看"监听端口"一行有没有 `LISTEN`。
2. 看 `/data/adb/daidai-panel/service.log`（宿主侧启动日志）有没有 "面板启动失败"。
3. 进容器看 `/app/Dumb-Panel/daidai.log`（容器内后端日志）是否 panic 或端口被占。
4. MIUI / OriginOS / ColorOS 等激进省电策略会冻结后台进程，把管理器 daemon（`magiskd` / `ksud`）加入电池白名单；或打开浏览器时勾选"允许后台"。

**Q: 改了 `ports.conf` 但端口没生效**

`service.sh` 检测到 `daidai-server` 已在跑会直接跳过，光重跑 `service.sh` 是不行的。必须先 `pkill` 再重跑：

```bash
su -c "pkill -f daidai-server; sh /data/adb/modules/daidai-panel/service.sh"
```

**Q: 升级后旧数据会丢吗？**

不会。升级流程：`customize.sh` → 备份 `<rootfs>/app/Dumb-Panel/` 到 `TMPDIR/backup_data/` → 清旧 rootfs → 重装 Alpine + 重装依赖 → 把备份复原回去。`ports.conf` 在宿主侧的 `/data/adb/daidai-panel/` 不受影响。

**Q: 禁用模块之后面板还在跑？**

对，禁用 = 下次开机 Magisk 不挂载模块，不等于 kill 进程。`daidai-server` 是 `rurima` 启的独立进程树，和模块本身解耦。立即停用：`su -c "pkill -f daidai-server"`。

**Q: 能用面板内的"检查系统更新"一键更新吗？**

不能——那是 Docker 版专属（要挂 `docker.sock`）。Magisk 版走 `module.prop` 里的 `updateJson` 链路，见上面 [一键更新](#在模块卡片内一键更新) 章节。
