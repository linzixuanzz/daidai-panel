# 呆呆面板 Magisk 模块

通过 Magisk / KernelSU / APatch 在已 Root 的 Android 设备上运行呆呆面板。开机自启，浏览器访问 `http://127.0.0.1:5700` 即可使用；后端绑定 `0.0.0.0`，局域网 / 内网穿透也能直连。

> 本模块无需 Docker、无需 Termux。安装阶段会下载一份 rootfs 到 `/data/daidai`，在容器里用包管理器装好 Python / Node.js / git 等运行时后，把 `daidai-server` 放进容器启动。运行期等同于"用 root 起了一个极小号 Linux 容器跑面板"。

---

## 两个版本：Alpine 还是 Debian？

从 v3.0.0 起每次发版会同时产出两个 ZIP，**装哪个由你决定，不能同时装**（两者是同一个模块 id，后装的会覆盖先装的，但用户数据照常保留）。

| | `daidai-panel-magisk-vX.Y.Z.zip` | `daidai-panel-magisk-debian-vX.Y.Z.zip` |
|---|---|---|
| 容器基础系统 | Alpine 3.18（musl） | Debian 12 bookworm（glibc） |
| rootfs 下载 | NJU 镜像站，约 3 MB | GitHub Release 资产，约 27 MB |
| 包管理器 | `apk` | `apt-get` |
| 装完占用 | 约 300–500 MB | **约 800 MB**（实测） |
| 磁盘要求 | ≥ 1.5 GB | ≥ 2.5 GB |
| Python / Node | 3.11 / 18.x | 3.11 / 18.x（**持平，不是升级**） |
| 跑 glibc 预编译产物 | ❌ | ✅ |

**默认选 Alpine**：体积小、装得快、下载量小，绝大多数定时任务脚本够用。

**什么时候选 Debian**：需要跑 glibc 预编译产物的时候。最典型的就是面板「依赖管理」里的「**一键安装 Python / Node 运行时**」——它下发的是 `python-build-standalone` 的 `*-unknown-linux-gnu` 构建和 nodejs.org 官方 Linux 构建，两者都硬依赖 `/lib64/ld-linux-*.so`。实测在 Alpine(musl) 容器里 **0/2 可执行**（报 `not found`，其实是缺 glibc 动态链接器），只有在 Debian 容器里才真正可用。

同理，只提供 manylinux wheel 的 Python 包、只提供 glibc 二进制的商用脚本，也都需要 Debian 版。

> ⚠️ **已知限制：Debian 版没有模块卡片一键更新。**
> `module.prop` 的 `updateJson` 里只能填一个 `zipUrl`，而两个 flavor 共用同一个模块 id，所以一键更新只会指向 **Alpine** 的 ZIP。
> **Debian 用户请不要点模块卡片上的「更新」按钮**（点了会把你换成 Alpine 版），改为每次手动到 [Release 页](https://github.com/linzixuanzz/daidai-panel/releases) 下载 `daidai-panel-magisk-debian-vX.Y.Z.zip` 安装。数据同样会被保留。

> ⚠️ **Debian 版尚未经过真机验证。** 它在 CI 上能打出包、脚本能通过静态检查，但项目里没有 Android 真机测试基建，Debian 容器在真实设备上能否起来、`apt-get` 在 chroot 里是否顺利，暂时无法证明。遇到问题请带上安装日志开 issue。

---

## 架构与目录（必读）

和 Docker 版的 Nginx + Go 两层结构不同，Magisk 版只有一层：前端静态资源由 `daidai-server` 在单端口（默认 `5700`）上直接托管。整个运行时放进容器（通过仓库自带的 `rurima` 进入），数据 / 日志 / 脚本都落在容器内路径。

```
/data/adb/modules/daidai-panel/        ← 模块本体（随模块卸载清除）
├── module.prop
├── flavor                             ← 容器基础系统标记（alpine / debian）
├── customize.sh / service.sh / uninstall.sh / action.sh
├── scripts/check-runtimes.sh
└── system/bin/
    ├── rurima                         ← 容器运行时（静态二进制）
    ├── daidai-server                  ← 后端（每次开机同步进容器）
    └── ddp                            ← CLI（同上）

/data/daidai/（或 /data/local/daidai/）← 容器 rootfs，Alpine 占 300MB+ / Debian 占 600MB+
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
  - Magisk **v20.4+**（v20–v23 可装但缺少模块卡片一键更新；推荐 v24+）
  - KernelSU
  - APatch
- Android 6.0 (API 23) 及以上。**建议 Android 8.0+**；Android 6.x / 7.x 属于「可以尝试」而不是「保证可用」——少数机型受 SELinux 策略 / 内核挂载限制起不了容器。安装过程中会**实际探测一次容器能否启动**，起不来会当场中止并说明原因，不会让你装完重启后才发现用不了
- CPU 架构：**仅 `arm64`（aarch64）**。x86_64 设备会在安装时被明确拦下——模块自带的容器运行时 `rurima` 只有 aarch64 构建，在 x86_64 上无法执行
- 剩余可用空间：**Alpine 版 ≥ 1.5 GB**（rootfs ~300 MB + 依赖 + 数据 / 日志）；**Debian 版 ≥ 2.5 GB**（rootfs 解压 ~103 MB，装完 python / node / npm / git / 构建工具并清掉 apt 缓存后，根文件系统实测约 800 MB）
- **安装阶段需要联网**（下载 rootfs + 联网装 python3 / nodejs / git 等；Alpine 侧累计约 50 MB，Debian 侧约 300 MB）

## 安装

1. 下载 ZIP（或按[下面章节](#本地构建)自行构建）：
   - Alpine（默认，推荐）：`daidai-panel-magisk-vX.Y.Z.zip`
   - Debian（需要 glibc 时）：`daidai-panel-magisk-debian-vX.Y.Z.zip`
2. 打开 Magisk / KernelSU / APatch 管理器 →「模块」→「从本地安装」，选该 ZIP。
3. 等几分钟，rootfs 下载 + 容器能力探测 + 装依赖 + 运行时验证全部通过后，才会出现 "安装完成！" 提示。中途任何一步失败都会当场中止并说明原因——**看到 "安装完成！" 就代表环境确实可用**。安装日志开头会打印 `容器基础系统：...`，可以据此确认自己装的是哪个版本。
4. 重启手机。
5. 手机浏览器访问 `http://127.0.0.1:5700`，按提示初始化管理员账号。

## 在模块卡片内一键更新

模块 `module.prop` 里已经填好了 `updateJson`：

```
updateJson=https://github.com/linzixuanzz/daidai-panel/releases/latest/download/update.json
```

这是 **GitHub Release 的稳定跳转地址**，会自动指向"当前最新一次 Release"里随附的 `update.json`。因此：

1. 每次仓库推送新的 `vX.Y.Z` tag，工作流会自动：
   - 编译 arm64 静态后端
   - 打包 `daidai-panel-magisk-vX.Y.Z.zip`（Alpine）与 `daidai-panel-magisk-debian-vX.Y.Z.zip`（Debian）
   - 导出并上传 `daidai-debian-rootfs-arm64.tar.gz`（Debian 版安装时下载的 rootfs）
   - 生成指向本次 Release 的 `update.json` 与 `update-debian.json`（含版本号 / versionCode / zipUrl / changelog）
   - 把这些文件一起上传到 Release
2. 已装旧版本的手机，打开管理器时自动拉取对应的 update json，比 `versionCode` 发现有新版 → 模块卡片出现「**更新**」按钮
3. 点按钮 → 管理器自动下载 ZIP 并走安装流程（等同手动「从本地安装 ZIP」）
4. 重启手机完成升级。升级流程内部：`customize.sh` 先把容器里的 `/app/Dumb-Panel/` 整个备份到 `/data/adb/daidai-panel/update-data-backup`，然后清掉旧 rootfs 重装容器，装完再把备份复原回去——数据库、脚本、日志、依赖全部保留；如果下载或安装中途失败，下次重试会优先沿用这份备份恢复

> 自 `v3.0.3` 起，两个 flavor 各有各的更新地址：Alpine ZIP 里的 `module.prop` 指向 `update.json`，Debian ZIP 指向 `update-debian.json`（由 `Magisk/build.sh` 在打包时改写）。**Debian 用户在管理器里点「更新」不会再被静默换成 Alpine 版。**
>
> 历史行为（v3.0.2 及更早）：`updateJson` 只能填一个 `zipUrl`，两个 flavor 又共用同一个模块 id，「更新」按钮永远指向 Alpine ZIP，Debian 用户点了会被换成 Alpine 版（数据不丢，但 glibc 容器没了）。从 v3.0.2 升上来的 Debian 用户，需要先手动刷一次 v3.0.3 的 Debian ZIP，之后管理器才会走对地址。

### 面板内在线升级（v3.0.3+）

刷模块 ZIP 会清掉整个 rootfs 重装容器，还要重启手机、重新下载约 300MB 依赖。日常升级不必这么重：

进入面板「系统设置 → 概览 → 检查系统更新」，模块版会显示「Magisk 模块版在线升级」，点「立即更新」即可。它只做三件事：

- 替换容器内的 `/usr/local/bin/daidai-server`、`/usr/local/bin/ddp`
- 替换前端目录 `/app/web`
- 同时把这几样写回模块目录并同步 `module.prop` 的版本号，保证重启后不回滚

容器 rootfs、apt/apk 装的系统包、Python venv 与已装依赖、`config.yaml`、`ports.conf` 一概不动，**不需要重启手机**。

**在线升级覆盖不到模块外壳**（`service.sh` / `customize.sh` / `action.sh` / rootfs 结构）。也就是说升完之后你拿到的是「新面板程序 + 旧模块脚本」，管理器里显示的版本号也已经是新版 —— 这是正常的，面板照常工作，只是外壳带来的新能力拿不到。

需要重刷模块 ZIP 的情况：

1. 想用某个**由模块脚本实现**的新能力。例如 v3.0.4 的「停止面板服务」：它靠新版 `service.sh` / `action.sh` 实现，在线升级上来的用户面板里那个按钮会显示为禁用，并提示当前外壳版本。
2. 某个版本的新面板**无法**在旧外壳上运行。这种情况面板会在检查更新时直接拒绝并提示重刷 ZIP，不会硬装上去（判据是后端的 `requiredMagiskShellVersion`，只有真正不兼容时才会提高）。
3. 从 v3.0.2 或更早版本升级——在线升级能力本身要先随 v3.0.3 装上，所以这一跳必须刷 ZIP。

> 说明：需要管理器版本支持 `updateJson`（Magisk v24.0+、KernelSU、APatch 新版均支持）。如果你自己 fork 了本项目发版，请把 `module.prop` 里的 `linzixuanzz/daidai-panel` 替换成自己的仓库路径即可；`customize.sh` 里 Debian rootfs 的下载地址同样写着这个仓库路径，也要一起改。

### 手动触发更新检查

部分管理器默认只在打开模块列表时刷新一次。想立即触发，可以：

- **Magisk**：在「模块」页面下拉刷新
- **KernelSU / APatch**：在「模块」页面点右上角的刷新图标

如果希望强制下载最新 ZIP（比如想跳过 versionCode 比较），也可以直接从 Release 页下载 ZIP 手动安装，数据目录同样不会被清。

---

## 脚本运行时

`customize.sh` 会在安装阶段进容器执行一遍包安装，两个 flavor 装的是同一套能力，只是包名不同：

| 用途 | Alpine (`apk add`) | Debian (`apt-get install`) |
|------|--------------------|----------------------------|
| Shell | `bash` `bash-completion` | `bash` `bash-completion` |
| 基础工具 | `coreutils` | `coreutils` |
| 编译工具链 | `build-base` `libtool` | `build-essential` `libtool` |
| Python | `python3` `python3-dev` `py3-pip` | `python3` `python3-dev` `python3-pip` **`python3-venv`** |
| Node.js | `nodejs` `npm` | `nodejs` `npm` |
| 网络工具 | `curl` `wget` `git` `jq` `netcat-openbsd` | `curl` `wget` `git` `jq` `netcat-openbsd` |
| SSH / TLS | `openssh` `openssl` | `openssh-client` `openssh-server` `openssl` **`ca-certificates`** |
| 用户 / 系统 | `shadow` `tzdata` `procps` + 离线 `linux-pam` | `passwd` `tzdata` `procps`（PAM 由 openssh-server 带入） |

加粗的两个是 Debian 独有的补充项：bookworm 把 `ensurepip` 拆到了 `python3-venv`（没有它 `python3 -m venv` 直接失败，而面板每次开机都要建 venv）；`debian:bookworm-slim` 不带根证书，`ca-certificates` 不装的话 `pip` / `npm` / `git` 走 HTTPS 会全线报错。

npm 默认镜像源两边都已切到 `npmmirror.com`；apk / apt 源都指向 `mirrors.nju.edu.cn`。

面板「依赖管理」页的 `pip` / `npm` 直接可用；定时任务跑 Python / Node.js / Shell / Git 脚本无需额外配置，**不需要 Termux，也不需要自备静态二进制**。

> **Alpine 版是 musl 基础**：遇到只有 glibc 预编译包（例如某些商用脚本自带的 `.whl`、面板自带的「一键安装运行时」）时，请改用源码安装、找 musl wheel，或直接换 Debian 版。
>
> **Debian 版是 glibc 基础**：manylinux wheel、官方 Node / Python 预构建都能直接跑。bookworm 同样遵循 PEP 668（系统 Python 被标记 externally-managed），但面板的依赖管理已经处理过这件事——它优先用 `deps/python/<小版本>` 下的托管 venv，落到系统 pip 时会自动补 `--break-system-packages --user`。只有你自己 `su` 进容器手敲 `pip install` 时才会撞上这个报错，那种情况请自己建 venv。

---

## 动作按钮 = 停止 / 启动 + 状态摘要（推荐）

模块内置 `action.sh`。在 **Magisk v26+ / KernelSU / APatch** 的模块列表里，呆呆面板条目右侧会出现「运行 / Action」按钮。

### 它是一个 toggle（自 v3.0.4）

管理器的动作按钮只能「无参数、单次执行、回显 stdout」，不能传参也不能交互，所以停止和启动共用这一个按钮：

| 点击时面板 | 本次动作 |
|-----------|---------|
| 正在运行 | **停止**：写下停止开关 → 结束面板进程（先 TERM 后 KILL）→ 释放 wake_lock |
| 未在运行 | **启动**：删掉停止开关 → 重跑 `service.sh`（同步模块文件 + 拉起容器 + 起新的存活守护） |

判定**以进程状态为准**：面板刚崩掉但没有停止开关时，本次执行的是「启动」。每次点击的输出最前面都会打印这次到底做了什么。

**停止状态跨重启保持**：开关文件在 `/data/adb/daidai-panel/stopped`，重启手机后 `service.sh` 会同步完模块文件就直接退出，不会把面板拉起来。再点一次动作按钮即可启动。

停止时**不动**这两样：

- 容器内 `sshd` 不停 —— 面板停了 Web 也没了，SSH 是唯一的排障退路
- `ruri` 挂载不卸载 —— 卸载会让下次「启动」变成一次完整的容器重入

> 也可以在面板里操作：设置 → 概览 → 「停止面板服务」。该按钮只在模块版显示；通过面板内在线升级上来的用户模块脚本还是旧版，按钮会显示为禁用并提示重刷一次模块 ZIP。

### 同一次点击还会打印

- 当前容器基础系统（`alpine` / `debian`）——忘了自己装的哪个版本时看这一行
- 当前端口配置（`PANEL_PORT` / `SSH_PORT` / `EXTRA_CORS_ORIGINS`）
- 面板进程状态 + PID（动作执行**之后**的实际状态）
- 宿主侧 `PANEL_PORT` 与 `SSH_PORT` 的实际监听情况
- 容器运行时自检（`python3` / `node` / `npm` / `git` / `curl` / `bash` 的路径与版本）
- 容器 SSH 自检（`sshd` 版本、`sshd_config`、特权分离用户、host key 数量、root 密码是否已设置、`sshd -t` 结果）
- `service.log`（宿主侧启动日志）最近 60 行
- `daidai.log`（容器内后端日志）最近 60 行
- 容器内 `service.log` 最近 30 行（含每次开机写的 SSH 状态快照）与 `sshd.log` 最近 30 行

排障的第一步永远是先点这个按钮看输出，不用 adb 连线。

## 常用操作

```bash
# 宿主侧 —— 启动日志
su -c "tail -f /data/adb/daidai-panel/service.log"

# 进入容器（获得完整 bash / apk 或 apt / python / node / git / ddp）
MODDIR=/data/adb/modules/daidai-panel
ROOTFS=/data/daidai                   # 少数设备在 /data/local/daidai
su -c "$MODDIR/system/bin/rurima ruri -p -N -S -A $ROOTFS /bin/bash"
```

> 这里用 `/bin/bash` 是**两个 flavor 通用**的（两边都装了 bash）。模块脚本内部用的是
> `$MODDIR/flavor` 决定的 `/bin/ash`（Alpine）或 `/bin/bash`（Debian）——
> **Debian 容器里没有 `/bin/ash`**，手敲命令时别照抄旧文档里的 `ash`。

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

**点两次模块卡片的「运行 / Action」按钮** —— 第一次停止，第二次启动。等价的命令行写法：

```bash
# 与按钮完全等价的 toggle，跑两次 = 停 + 起
su -c "sh /data/adb/modules/daidai-panel/action.sh"
```

> ⚠️ 别再用 `su -c "pkill -f daidai-server; sh .../service.sh"`（v3.0.3 及更早的文档里是这么写的）。两个问题：
>
> 1. 自 v3.0.3 起 `service.sh` 会 fork 一个存活守护，`pkill` 掉的面板 60 秒内就会被它拉回来；
> 2. 执行这条命令的 `sh -c` 进程自己的 cmdline 里就含 `daidai-server`，会被 `pkill -f` 命中，分号后半句多半根本没执行到（用户看到「面板过一会儿又回来了」其实是守护拉的）。
>
> `action.sh` 用的是完整路径 `/usr/local/bin/daidai-server` + `kill <pid>`，并且会先写停止开关让守护自退，不存在这两个问题。

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

> **模块每次开机会按 `ports.conf` 重写容器里的 SSH 配置**（自 v3.0.7 起）：`sshd_config` 中的
> `Port` / `PermitRootLogin` / `PasswordAuthentication` 三项会被删净后重新写入，
> Debian 容器还会同步写一份 `/etc/ssh/sshd_config.d/00-daidai.conf`
> （它的 `sshd_config` 顶部有未注释的 `Include`，而 OpenSSH 是「**第一次取到的值胜出**」，
> 不写这份 drop-in 的话主文件里的配置随时可能被静默覆盖）。
>
> 这与面板的 `config.yaml` 是同一套逻辑：**由 `ports.conf` 单向决定，不保留手改**。
> 所以如果你把容器里的 SSH 改成了密钥登录、或手动关掉了密码认证，
> 下次开机会被改回来。其余指令（`AuthorizedKeysFile`、`AllowUsers`、host key 配置等）不受影响，
> 只有上面这三项是模块托管的。

**生效方式**（任选其一）：

- 方式 1：重启手机，`service.sh` 开机自动重跑
- 方式 2（不用重启手机）：**点两次模块卡片的「运行 / Action」按钮** —— 第一次停止，第二次启动

```bash
# 方式 2 的命令行等价写法，跑两次 = 停 + 起
su -c "sh /data/adb/modules/daidai-panel/action.sh"
```

> 单独再跑一次 `service.sh` 是**无效的**——它检测到 `daidai-server` 已在跑会直接跳过（避免重复拉起）。必须先让旧进程退出，新进程才会按新 `ports.conf` 重新生成 `config.yaml` 并绑定新端口。而「让旧进程退出」现在只能走 `action.sh`：直接 `pkill` 的话，存活守护 60 秒内就把它拉回来了。

改完后想确认实际监听状态，点「运行 / Action」按钮即可看到 `PANEL_PORT` / `SSH_PORT` 的当前监听情况。⚠️ 注意这个按钮自 v3.0.4 起同时是停止 / 启动开关：**每点一次都会切换面板的运行状态**（在跑就停、没跑就起），输出最前面的「本次动作」会写清这次做了什么，按需再点一次即可切回来。

## 对系统的影响

本模块是**纯用户态 / 非侵入式**的：

| 类别 | 是否触碰 | 说明 |
|------|----------|------|
| `/system` 分区 | ❌ | 不修改系统文件，纯 Magisk 魔挂 |
| `system.prop` / `sepolicy.rule` | ❌ | 不写系统属性、不加 SELinux 规则 |
| 应用安装 / 广告 / 服务伪装 | ❌ | 不装 APK、不注册账户、不开后台伪装 |
| 网络监听 | ⚠️ | 绑定 `0.0.0.0:PANEL_PORT`（默认 5700）+ 容器内 `sshd` 监听 `0.0.0.0:SSH_PORT`（默认 22），局域网任何人都能尝试连接 |
| 写入位置 | ✅ | 三处：`/data/adb/modules/daidai-panel/`（模块本体）、`/data/daidai/` 或 `/data/local/daidai/`（容器 rootfs + 所有用户数据，占大空间）、`/data/adb/daidai-panel/`（端口配置 + 启动日志 + 停止开关） |

> **局域网可见性**：面板后端默认对局域网开放。家里 / 自己 WiFi 没问题；公共网络（咖啡馆、公司 Guest Wi-Fi）建议把 `SSH_PORT` 与默认密码都换掉，并在路由器 / 防火墙层面限制面板端口。
>
> 想临时停掉容器内的 `sshd`：`su -c "pkill -x sshd"`（容器里没有 openrc / systemd，`rc-service` 与 `systemctl` 都不可用）。
>
> 下次开机 `service.sh` 会重新拉起它 —— **但前提是面板不处于「已停止」状态**：点过动作按钮停面板之后，`service.sh` 会在停止开关那里早退，SSH 也不会被拉起。要在停止状态下恢复 SSH，得先点一次动作按钮把面板启动起来。

> **禁用 ≠ 停服**：在管理器里「禁用」模块只阻止下次开机加载，**不会 kill 当前的容器进程**（`daidai-server` 是 `rurima` 启的独立进程树）。想立即停：**点模块卡片的「运行 / Action」按钮**（面板在跑时这一次点击就是停止），或在面板里点「设置 → 概览 → 停止面板服务」。
>
> ⚠️ `su -c "pkill -f daidai-server"` 自 v3.0.3 起就**停不掉**了：`service.sh` fork 的存活守护每分钟探活一次，60 秒内就会把它拉回来（刚拉起过的话最坏要等 5 分钟，看起来像是随机复活）。

## 卸载（默认彻底清理，不留痕迹）

1. 在 Magisk / KernelSU / APatch 管理器中移除本模块
2. 重启手机

重启完成后 `uninstall.sh` 会自动做：

- `TERM` + `KILL` 掉仍在运行的 `daidai-server` 进程
- 删除容器 rootfs `/data/daidai` 和 `/data/local/daidai`（数百 MB ~ 1 GB，**面板所有数据都在这里**；两个 flavor 路径相同）
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
# 默认只打 arm64 + alpine（CI 发布用的也是这个）
bash Magisk/build.sh 3.2.7

# 只打 amd64
bash Magisk/build.sh 3.2.7 amd64

# 同时打 arm64 + amd64
bash Magisk/build.sh 3.2.7 all

# Debian flavor（第 3 个参数；不传就是 alpine）
bash Magisk/build.sh 3.2.7 arm64 debian
```

> `amd64` / `all` 保留的是**构建能力**，不代表模块支持 x86_64：容器运行时 `rurima` 只有 aarch64 构建，`customize.sh` 会在 x86_64 设备上直接拦截。这两个参数是为「将来拿到 x86_64 的 rurima」留的口子，日常发布请用默认的 `arm64`。

构建产物：

| flavor | 产物 |
|--------|------|
| `alpine`（默认，不传第 3 个参数） | `dist/daidai-panel-magisk-v<版本>.zip` |
| `debian` | `dist/daidai-panel-magisk-debian-v<版本>.zip` |

`module.prop` 里的 `version` / `versionCode` 会自动按参数同步。两个 ZIP 唯一的结构差异是：Debian 包里的 `flavor` 文件内容是 `debian`，且**不含** `apk/` 目录（那两个离线包是 aarch64 Alpine 专用）。

> **本地打 Debian 包能直接装**：`customize.sh` 里的 rootfs 地址是
> `releases/latest/download/daidai-debian-rootfs-arm64.tar.gz` 这个固定跳转，
> 只要仓库已经发过一次带该资产的 Release，本地构建的未发布版本也能正常下载 rootfs。

前置依赖：

- **Go 1.22+**（静态编译 `CGO_ENABLED=0`）
- **Node.js 20+**（首次构建自动跑 `npm ci && npm run build`，已有 `web/dist` 会跳过）

> ⚠️ **跑过 `npm run build:demo` 之后别直接打包**：在线演示 Demo 与发布版写的是同一个
> `web/dist`，Demo 产物里有整套浏览器内 mock 层，还带 `/daidai-panel/` 的 base 前缀。
> 直接复用它打出来的模块「能开、数据全是假的、一个错都不报」。
> `build.sh` 在复用已存在 dist 时会校验这三点（mock 哨兵 / robots / 根相对路径）并直接报错退出，
> 按提示 `rm -rf web/dist` 后重跑即可。它**不会**替你重建 —— 静默重建等于把一次误操作
> 换成一次你不知情的几分钟构建。
>
> 想单独确认手上的 `web/dist` 是哪一种，跑 `bash Magisk/build.sh --check-dist`：
> 只做这一道校验就退出，不构建、不打包，也不要求 Go 在 PATH 上。
- `zip` 或 `python3`（Windows Git Bash 下没有 `zip` 时会 fallback 到 Python 打包）
- 仓库自带的 `Magisk/system/bin/rurima`（~720 KB 静态二进制，打包时会拷进 ZIP）

## FAQ

**Q: 安装时卡在"正在联网下载 ... rootfs" / "正在联网安装面板运行依赖"**

这两步强依赖网络：

| | rootfs 下载 | 装依赖 |
|---|---|---|
| Alpine | ~3 MB，NJU 镜像站 | 约 50 MB，`apk` 走 `mirrors.nju.edu.cn` |
| Debian | ~27 MB，GitHub Release | 约 300 MB，`apt-get` 走 `mirrors.nju.edu.cn` |

公司 / 学校网络被墙的话挂 VPN 重装即可。Debian 版还多一层：rootfs 放在 GitHub Release 上，国内直连 GitHub 不稳定的话这一步更容易失败。

两步都有失败保护，但机制不同：

- **下载 / 解压 rootfs**：失败直接 abort。
- **装依赖**：`apk add` / `apt-get install` 都可能「部分成功」，光看退出码不可靠，所以装完后会再进一次容器逐个验证 `python3` / `node` / `npm` / `git` / `bash` 能否执行并报出版本。任一缺失就 abort，并列出**具体缺了哪些**。这条验证对两个 flavor 用的是同一份清单。

两种情况都不会损坏已有数据：升级安装时用户数据在清 rootfs 之前就已经备份到 `/data/adb/daidai-panel/update-data-backup/`，abort 后备份原样保留，下次安装会自动恢复。

**Q: 浏览器打不开 `http://127.0.0.1:5700`**

1. 先点模块卡片「运行」按钮，看"监听端口"一行有没有 `LISTEN`。
2. 看 `/data/adb/daidai-panel/service.log`（宿主侧启动日志）有没有 "面板启动失败"。
3. 进容器看 `/app/Dumb-Panel/daidai.log`（容器内后端日志）是否 panic 或端口被占。
4. MIUI / OriginOS / ColorOS 等激进省电策略会冻结后台进程，把管理器 daemon（`magiskd` / `ksud`）加入电池白名单；或打开浏览器时勾选"允许后台"。

**Q: 改了 `ports.conf` 但端口没生效**

`service.sh` 检测到 `daidai-server` 已在跑会直接跳过，光重跑 `service.sh` 是不行的。必须先让旧进程退出。

**点两次模块卡片的「运行 / Action」按钮**（第一次停、第二次起）即可，或用等价命令：

```bash
# 跑两次 = 停 + 起
su -c "sh /data/adb/modules/daidai-panel/action.sh"
```

⚠️ 别用 `pkill -f daidai-server`：存活守护 60 秒内就会把它拉回来，而且执行这条命令的 `sh -c` 自身就会被 `pkill -f` 命中。

**Q: 升级后旧数据会丢吗？**

不会。升级流程：`customize.sh` → 备份 `<rootfs>/app/Dumb-Panel/` 到 `/data/adb/daidai-panel/update-data-backup/` → 清旧 rootfs → 重装 rootfs + 重装依赖 → 把备份复原回去。若安装中途失败，保留下来的 `update-data-backup` 会在下次安装时优先恢复。`ports.conf` 在宿主侧的 `/data/adb/daidai-panel/` 不受影响。

**Q: 能从 Alpine 版直接换成 Debian 版吗？**

可以，直接装另一个 ZIP 即可，走的是和升级完全一样的流程：旧数据先备份到 `/data/adb/daidai-panel/update-data-backup/`，旧 rootfs 整个删掉，装完新 rootfs 再把数据复原回去。数据库、脚本、备份都保留。

**但有两样东西不会跟过去**：

1. `deps/` 里已经装好的 pip / npm 依赖 —— musl 和 glibc 编译出来的扩展互不兼容，换过去之后需要在面板「依赖管理」里重新安装一遍
2. 你手动在容器里 `apk add` / `apt install` 装过的东西 —— rootfs 是整个重建的

换完记得点模块卡片的「运行」按钮，确认 `容器基础系统` 那一行已经变了。

**Q: 禁用模块之后面板还在跑？**

对，禁用 = 下次开机 Magisk 不挂载模块，不等于 kill 进程。`daidai-server` 是 `rurima` 启的独立进程树，和模块本身解耦。

立即停用：**点模块卡片的「运行 / Action」按钮**（面板在跑时这一次就是停止），或在面板里点「设置 → 概览 → 停止面板服务」。停止状态跨重启保持，再点一次按钮即可启动回来。

⚠️ `su -c "pkill -f daidai-server"` 停不掉：`service.sh` fork 的存活守护 60 秒内就会重新拉起面板（刚拉起过则最坏 5 分钟）。

**Q: 能用面板内的"检查系统更新"一键更新吗？**

不能——那是 Docker 版专属（要挂 `docker.sock`）。Magisk 版走 `module.prop` 里的 `updateJson` 链路，见上面 [一键更新](#在模块卡片内一键更新) 章节。
