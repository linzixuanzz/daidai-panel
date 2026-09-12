<p align="center">
  <img src="./images/图标.png" alt="呆呆面板" width="120">
</p>

<h1 align="center">呆呆面板</h1>

<p align="center">
  <em>轻量、现代的定时任务管理面板，Docker 一键部署，开箱即用</em>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Vue-3-4FC08D?logo=vue.js&logoColor=white" alt="Vue3">
  <img src="https://img.shields.io/badge/Element%20Plus-2.x-409EFF?logo=element&logoColor=white" alt="Element Plus">
  <img src="https://img.shields.io/badge/SQLite-3-003B57?logo=sqlite&logoColor=white" alt="SQLite">
  <img src="https://img.shields.io/badge/Docker-Ready-2496ED?logo=docker&logoColor=white" alt="Docker">
</p>

<p align="center">
  <a href="https://linzixuanzz.github.io/daidai-panel/"><img src="https://img.shields.io/badge/在线演示-点开即用-2EA44F?logo=github&logoColor=white" alt="在线演示"></a>
</p>

---

呆呆面板 (Daidai Panel) 是一款轻量级定时任务管理平台，采用 Go (Gin) + Vue3 (Element Plus) + SQLite 架构，专注于脚本托管与自动化任务调度。支持 Python、Node.js（含 `.js` / `.mjs`）、Shell、TypeScript、Go 等多语言脚本的定时执行与可视化管理，内置 22 种消息推送渠道、订阅管理、环境变量、依赖管理、Open API 等功能。Docker 一键部署，开箱即用。

> 最新稳定版：`v3.2.7` · [更新日志](./docs/release-notes/v3.2.7.md)<br>
> 本次重点：运行中的任务排到前面（但点运行不会当场跳、刷新后才重排）；环境变量能按变量名精确筛选并显示同名条数；上传已有定时任务的脚本不再重复询问；「发现新版本」弹窗终于渲染 markdown<br>
> APP 客户端：[linzixuanzz/Dumb-Panel-APP](https://github.com/linzixuanzz/Dumb-Panel-APP)

## 在线演示

想先看看效果再决定装不装，直接打开 **<https://linzixuanzz.github.io/daidai-panel/>**，点「进入演示环境」就能逛完整面板 —— 不用注册，也不用装 Docker。演示站跟随每个正式版自动更新，打开看到的就是最新正式版的界面。

里面新建任务、编辑脚本、拖拽排序都会真的生效，所以更要先说清楚它是什么：

> **演示站不是一台真实运行的面板**，而是面板前端单独打包后配上浏览器内的模拟数据，没有后端，也不会真的执行任何脚本。<br>
> **你的所有操作只存在你自己的浏览器里，刷新页面即恢复初始状态**，不会保存，也不会被别人看到。<br>
> 因此**请不要在演示站里填写真实的 Cookie、密码或推送密钥**——那里没有能托管它们的服务端，填了也只是白填。

## 功能特性

- **定时任务** — Cron 表达式调度，支持重试、超时、定时停止、任务依赖、前后置钩子
- **脚本管理** — 在线代码编辑器，支持 Python、Node.js（含 `.mjs`）、Shell、TypeScript、Go，拖拽移动文件
- **执行日志** — SSE 实时日志流，历史日志查看与自动清理
- **环境变量** — 分组管理、拖拽排序、批量导入导出（兼容青龙格式）
- **订阅管理** — 自动从 Git 仓库拉取脚本，支持定期同步
- **依赖管理** — 可视化安装/卸载 Python (pip)、Node.js (npm) 依赖，以及 **Linux 系统包**（自动识别 `apk` / `apt` / `dnf` / `yum` / `microdnf` / `zypper`）；页面工具条里还有管理员专用的网页版「系统命令行」
- **通知推送** — Bark、Telegram、Server酱、企业微信、钉钉、飞书等 22 种渠道
- **开放 API** — App Key / App Secret 认证，支持第三方系统对接
- **系统安全** — 双因素认证 (2FA)、IP 白名单、登录日志、多设备会话管理
- **数据备份** — 一键备份与恢复，支持每天/每周/每月定时备份
- **系统监控** — 实时 CPU / 内存 / 磁盘监控，任务执行趋势统计

<details>
<summary><b>展开：逐模块的完整能力清单（订阅的白名单 / 黑名单 / 依赖规则匹配细节在这里）</b></summary>

### 定时任务管理
- 标准 Cron 表达式调度
- 常用时间规则快捷选择
- 任务启用/禁用状态切换
- 手动触发执行
- 任务超时控制与重试机制
- 前后置钩子（任务依赖链）
- 多实例并发控制

### 脚本文件管理
- 在线代码编辑器（语法高亮）
- 支持创建、重命名、删除文件
- 支持文件上传与拖拽移动
- 脚本版本管理
- 调试运行与实时日志输出
- 支持 `.mjs` 脚本调试与任务执行

### 执行日志
- SSE 实时日志流
- 执行状态追踪（成功/失败/超时/手动终止）
- 执行耗时统计
- 日志自动清理策略

### 环境变量
- 安全存储敏感配置
- 变量值脱敏显示
- 分组管理与拖拽排序
- 批量导入导出（兼容青龙格式）
- 任务执行时自动注入

### 订阅管理
- Git 仓库自动拉取
- 定期同步（Cron 调度）
- SSH Key / Token 认证
- 白名单/黑名单/依赖规则过滤（对应青龙 `ql repo` 的第 2/3/4 个参数，`,` 与 `|` 均可作分隔符，匹配方式是「子串包含」而非正则）
  - 白名单不仅筛选任务，还会参与实际检出范围：只有命中白名单的文件会落盘并建成定时任务。
  - 依赖规则同样参与检出：命中的文件会被拉取到脚本目录供主脚本调用，但**不会**建成定时任务，主脚本 require 的辅助库填这里即可，不必再塞进白名单。
  - 黑名单对两者都生效；白名单留空时视为全部命中，依赖规则不改变任何行为。

### 消息推送
- 22 种主流推送渠道
- 任务执行结果通知
- 系统事件告警
- 自定义推送模板

### 系统设置
- 双因素认证 (2FA / TOTP)
- IP 白名单
- 登录日志与多设备会话管理（可配置网页端 / APP 端最大会话数）
- 数据备份与恢复（含视图数据）
- 定时备份（每天 / 每周 / 每月）
- 面板标题与图标自定义

</details>

## 效果图

<details>
<summary><b>展开：12 张界面截图（仪表盘 / 定时任务 / 执行日志 / 脚本 / 订阅 / 系统设置…）</b></summary>

| 功能 | 截图 |
|------|------|
| 仪表盘 | ![仪表盘](./images/仪表盘.png) |
| 定时任务 | ![定时任务](./images/定时任务.png) |
| 执行日志 | ![执行日志](./images/执行日志.png) |
| 用户管理 | ![用户管理](./images/用户管理.png) |
| 脚本管理 | ![脚本管理](./images/脚本管理.png) |
| 环境变量 | ![环境变量](./images/环境变量.png) |
| 订阅管理 | ![订阅管理](./images/订阅管理.png) |
| 通知渠道 | ![通知渠道](./images/通知渠道.png) |
| Open API | ![Open API](./images/Open%20API.png) |
| 依赖管理 | ![依赖管理](./images/依赖管理.png) |
| 系统设置 | ![系统设置](./images/系统设置.png) |
| 个人设置 | ![个人设置](./images/个人设置.png) |

</details>

## 快速部署

面板官方推荐用 Docker 部署。下面的例子默认浏览器访问 `http://宿主机IP:5700`。

### 一键启动（Alpine 运行时）

```yaml
# docker-compose.yml
name: daidai-panel

services:
  daidai-panel:
    image: ${DAIDAI_PANEL_IMAGE:-linzixuanzz/daidai-panel:latest}
    container_name: daidai-panel
    restart: unless-stopped
    ports:
      - "5700:5700"                                # 宿主机端口:容器内 Nginx 端口
    volumes:
      - ./Dumb-Panel:/app/Dumb-Panel               # 面板数据目录，升级保留
    environment:
      - TZ=Asia/Shanghai
      - CONTAINER_NAME=daidai-panel
      - IMAGE_NAME=${DAIDAI_PANEL_IMAGE:-linzixuanzz/daidai-panel:latest}
      - PANEL_UPDATE_MANAGER=watchtower
      - WATCHTOWER_HTTP_API_URL=${WATCHTOWER_HTTP_API_URL:-http://watchtower:8080}
      - WATCHTOWER_HTTP_API_TOKEN=${WATCHTOWER_HTTP_API_TOKEN:-daidai-panel-watchtower-token}
      - WATCHTOWER_HTTP_API_PERIODIC_POLLS=true
    labels:
      - com.centurylinklabs.watchtower.enable=true

  watchtower:
    image: nickfedor/watchtower:latest             # 必须是 v1.20.0 或更新的版本，原因见下方提示
    container_name: daidai-watchtower
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    labels:
      - com.centurylinklabs.watchtower.enable=false
    environment:
      - WATCHTOWER_HTTP_API_TOKEN=${WATCHTOWER_HTTP_API_TOKEN:-daidai-panel-watchtower-token}
      - WATCHTOWER_HTTP_API_PERIODIC_POLLS=true
      - WATCHTOWER_HTTP_API_ENDPOINTS=update
    command:
      - --label-enable
      - --cleanup
      - --interval
      - "3600"
```

```bash
docker compose up -d
```

首次访问 `http://localhost:5700` 会进入管理员初始化。

> ⚠️ **Watchtower 必须是 `v1.20.0`（2026-07-21 发布）或更新的版本，否则面板里的「检查系统更新」用不了。**<br>
> 上面这份配置用的 `WATCHTOWER_HTTP_API_ENDPOINTS=update` 是 `v1.20.0` 才引入的环境变量，更早的版本只认命令行参数 `--http-api-update`。而 Watchtower 遇到不认识的环境变量是**静默忽略**的 —— 容器照常启动、日志也不报错，但 HTTP API 的 8080 端口从头到尾没打开，面板一点更新就只会拿到 `connection refused`。<br>
> 更容易踩的是第二点：Watchtower 自己打了 `com.centurylinklabs.watchtower.enable=false`（避免自己管自己），**所以它不会自我更新** —— 本地那份 `latest` 是哪天拉的就一直停在哪天的版本。**凡是在 `v1.20.0` 之前就部署过 Watchtower 的用户，升级面板后请先手动拉一次新镜像再重建它**：

```bash
docker compose pull watchtower
docker compose up -d watchtower

# 确认版本：Watchtower 启动时会把自己的版本号打进日志
docker logs daidai-watchtower 2>&1 | head -n 5
```

> 走国内镜像加速站的话还要留意：加速站上的 `latest` 有可能是很旧的缓存。`pull` 完版本仍然偏低时，可以临时改拉具体版本（例如 `nickfedor/watchtower:1.21.2`），或者临时直连 Docker Hub 拉一次。

<details>
<summary><b>展开：这份 compose 每一条在做什么 · Docker Hub 太慢怎么换镜像源 · 不想自动更新怎么删掉 Watchtower · docker run 等价写法</b></summary>

如果 Docker Hub 访问慢，可以设置一次 `DAIDAI_PANEL_IMAGE`，让 `image` 和 `IMAGE_NAME` 同时使用你信任的镜像加速地址；README 默认不再内置固定第三方镜像源。也可以到 [容器镜像监控](https://status.anye.xyz/) 查看更多 Docker Hub 镜像加速源状态，再选择可用地址填写。

这份 compose 已经是推荐的可直接上线版本：

1. 面板容器只挂业务数据目录 `./Dumb-Panel:/app/Dumb-Panel`
2. `docker.sock` 只暴露给 Watchtower，不暴露给面板容器
3. `DAIDAI_PANEL_IMAGE` 同时控制容器实际镜像和面板记录的 `IMAGE_NAME`，切换标签只改一处
4. 只有打了 `com.centurylinklabs.watchtower.enable=true` 标签的容器会被自动更新
5. Watchtower 自己显式打了 `com.centurylinklabs.watchtower.enable=false`，避免被这套规则误纳入管理；代价是**它不会自我更新**，本地镜像会一直停在最后一次手动 `docker compose pull watchtower` 的版本
6. `WATCHTOWER_HTTP_API_ENDPOINTS=update` 开放面板所需的更新入口；API 只在 Compose 内部网络使用，没有向宿主机开放端口。**这个环境变量要 Watchtower `v1.20.0`+ 才认识**，更早的版本只认命令行 `--http-api-update`，会把它静默忽略掉：端口不开、日志也不报错
7. `WATCHTOWER_HTTP_API_PERIODIC_POLLS=true` 保留定时轮询，`--interval 3600` 表示每 1 小时检查一次更新
8. `--cleanup` 会在更新后清理旧镜像；当前使用 `nickfedor/watchtower:latest` 兼容新版 Docker API，但要确认实际拉到的版本不低于 `v1.20.0`（浮动标签在部分国内镜像加速站上可能是陈旧缓存）

如果你不想自动更新，可以删除 `watchtower` 服务、`labels`、`PANEL_UPDATE_MANAGER=watchtower` 和 `WATCHTOWER_HTTP_API_*`，然后改成在宿主机手动执行：

```bash
docker compose pull
docker compose up -d
```

想用 `docker run` 而不是 compose，推荐等价方式是分别启动面板容器和 Watchtower 容器：

```bash
docker network create daidai-panel-net
WATCHTOWER_API_TOKEN=daidai-panel-watchtower-token

docker run -d --pull=always \
  --name daidai-panel \
  --network daidai-panel-net \
  --restart unless-stopped \
  -p 5700:5700 \
  -v "$(pwd)/Dumb-Panel:/app/Dumb-Panel" \
  -e TZ=Asia/Shanghai \
  -e CONTAINER_NAME=daidai-panel \
  -e IMAGE_NAME=linzixuanzz/daidai-panel:latest \
  -e PANEL_UPDATE_MANAGER=watchtower \
  -e WATCHTOWER_HTTP_API_URL=http://daidai-watchtower:8080 \
  -e WATCHTOWER_HTTP_API_TOKEN="$WATCHTOWER_API_TOKEN" \
  -e WATCHTOWER_HTTP_API_PERIODIC_POLLS=true \
  --label com.centurylinklabs.watchtower.enable=true \
  linzixuanzz/daidai-panel:latest

docker run -d \
  --name daidai-watchtower \
  --network daidai-panel-net \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e WATCHTOWER_HTTP_API_TOKEN="$WATCHTOWER_API_TOKEN" \
  -e WATCHTOWER_HTTP_API_PERIODIC_POLLS=true \
  -e WATCHTOWER_HTTP_API_ENDPOINTS=update \
  --label com.centurylinklabs.watchtower.enable=false \
  nickfedor/watchtower:latest \
  --label-enable \
  --cleanup \
  --interval 3600
```

`docker run` 写法同样要求 **Watchtower `v1.20.0`+**（`WATCHTOWER_HTTP_API_ENDPOINTS` 是那个版本才有的，老版本会静默忽略它、不开 8080 端口）。注意上面只有面板容器带了 `--pull=always`，Watchtower 容器没有，而且它自己也不会自我更新 —— 如果宿主机上早就存在一份旧的 `nickfedor/watchtower:latest`，`docker run` 会直接复用那份旧镜像。所以请先手动拉一次再建容器：

```bash
docker pull nickfedor/watchtower:latest
docker rm -f daidai-watchtower          # 已经建过的话先删掉，再重新执行上面那条 docker run

# 建好之后确认版本：Watchtower 启动时会把自己的版本号打进日志
docker logs daidai-watchtower 2>&1 | head -n 5
```

</details>

<details>
<summary><b>展开：该选哪个镜像标签 —— 要跑 Go 任务 / 装需要现场编译的依赖 / pip 报 Failed building wheel 怎么办 / 换 Debian 运行时 / 指定 Python 3.10、3.11 / 查 CPU 架构支持 / 本地源码构建</b></summary>

### 支持的 CPU 架构

镜像是 multi-arch manifest list，`docker pull` 时按你机器自动选对应平台：

| 架构 | 典型机器 |
|------|---------|
| `linux/amd64` | x86_64 服务器、PC、绝大多数 NAS |
| `linux/arm64` | 树莓派 4 / 5、Oracle ARM 云、Apple Silicon |
| `linux/386` | 32 位 x86 老 PC、瘦客户端（仅 `latest` / `latest-full`，Debian 镜像不支持） |
| `linux/arm/v7` | **v2.0.9 新增**：树莓派 2 / 3 / Zero 2W、老 ARMv7 盒子 / 路由器 / NAS |

### Alpine 与 Debian 运行时和镜像标签

镜像分成两个基础系统和两个工具档位。Alpine 使用 `apk`，体积更小；Debian 使用 `apt` 和 glibc，适合依赖 Debian/Ubuntu 软件包的脚本。

| 工具档位 | 默认包含 | 额外工具或限制 |
|----------|----------|----------------|
| 精简版 | 目标 Python 与 pip/venv、Node.js/npm、`apk` 或 `apt`、Git/SSH、bash、curl、Nginx 和基础运行库 | 不含 Go、Docker CLI、wget、C/C++ 编译链、make、Linux 头文件、pkg-config，**也不含 CMake** |
| 完整版 | 精简版的全部内容 | 额外包含 Go/gofmt、Docker CLI、wget、C/C++ 编译链、make、Linux 头文件和 pkg-config；**同样不含 CMake** |

自 `v3.0.0` 起，**Go 任务必须使用 `latest-full` 或 `debian-full`。** 安装需要现场编译原生扩展的 pip/npm 依赖时，也建议使用完整版。普通 Python、JavaScript、TypeScript 和 Shell 任务优先使用体积更小的精简版。

> ⚠️ **「换工具档位」和「换基础系统」是两件事，别混在一起。**<br>
> **换档位（精简版 → `latest-full` / `debian-full`）解决不了「缺 CMake」。** 完整版补的是 Alpine 的 `build-base` + `linux-headers` + `pkgconf`、Debian 的 `build-essential` + `linux-libc-dev` + `pkg-config` —— 有 gcc / g++ / make，但**两个档位都没有 CMake**。只要那个包真的要现场编译、且构建后端调 CMake，换成完整版之后日志照样停在 `CMake must be installed to build ...`。<br>
> **换基础系统（Alpine → Debian）反而常常直接绕开编译。** PyPI 上的科学计算包普遍只发认 glibc 的 `manylinux` wheel、不发认 musl 的 `musllinux` wheel（`opencv-python` 就是典型：有 `manylinux_2_17` 的 x86_64 / aarch64 wheel，一个 musllinux wheel 都没有）。这类包在 Alpine 上只能源码编译，换到 `debian` / `debian-full` 后 pip 直接下预编译 wheel，几秒装完、根本用不到 gcc 和 CMake。<br>
> **两条路都不通，才是真的要现场编译。** 到面板「依赖管理 → Linux」页签补装系统包：**Alpine 装 `build-base`、`linux-headers`、`cmake`；Debian 装 `build-essential`、`cmake`**（RHEL / openSUSE 系是 `gcc`、`gcc-c++`、`make`、`cmake`）。该页签的「安装编译工具链」按钮会按当前探测到的包管理器把这几个包名预填好，不用自己记。

#### pip 装某个包时报 `Failed building wheel` / `gcc: not found` / `CMake must be installed`

这类报错的含义都一样：**这个包在当前平台没有可直接使用的预编译 wheel，pip 回退到下载源码现场编译，而镜像里缺编译工具。** 出路有两条，按下面的顺序试：

1. **在 Alpine 镜像上先试换 Debian 版镜像。** 多数科学计算包只发 `manylinux` wheel，换到 `debian` / `debian-full` 后 pip 直接下预编译包，不用编译也就不用装工具链，通常是最省事的一条路。
2. **换完镜像仍然报编译失败，说明这个包连 `manylinux` wheel 都没有，只能现场编译。** 到「依赖管理 → Linux」页签点「安装编译工具链」，装完再重装那个 pip 包。已经在 Debian 上的直接从这一步开始。
3. **别指望换工具档位。** 完整版只多了 gcc/g++/make，`cmake` 两个档位都没有（见上一条提示）。只有「缺 gcc / make」这一类才是完整版能覆盖的，而那同样可以在 Linux 页签装工具链解决，不必重建容器。
4. **面板会替你判一次，但判得到什么取决于日志写了什么。** 日志里出现 `gcc: not found`、`command 'gcc' failed`、`CMake must be installed`、`No CMAKE_CXX_COMPILER could be found` 这类**明确点名了工具的缺失信号**时，面板会把「缺 C/C++ 编译工具链或 CMake」这个结论、连同上面两条出路一起写进依赖的失败原因里。但 pip 很多时候只吐一句 `Failed building wheel` / `Failed to build installable wheels`、完全不提是哪个工具没有 —— 这种日志面板只能判到「musl 上没有预编译包」，给出的结论是先换 Debian 版镜像（对 `opencv-python` 这类包这恰好就是对的）；若换完仍失败，回到第 2 条装工具链。
5. **设了 `PUID` / `PGID` 的容器装不了系统包。** 包管理器要写 `/usr` 和自己的锁，必须 root；面板会明确说明而不是甩 `Permission denied`，并且不会再叫你去点那个点不动的按钮。此时在宿主机执行 `docker exec -u 0 <容器名> apk add build-base linux-headers cmake`（Debian 版镜像是 `apt-get install -y build-essential cmake`）。
6. **编译很慢，注意别撞超时。** 依赖安装默认 20 分钟超时，ARM 设备上现场编译 opencv 远不止这个时间。到「系统设置」里把「依赖安装超时(分钟)」调大即可，**上限 720 分钟（12 小时）**。
7. **想自己看真实报错**，可以用依赖管理页工具条菜单里的「系统命令行」（仅管理员可用）直接在容器里跑命令，不必先 `docker exec` 进容器。

#### 正式浮动标签与固定版本标签

自 `v3.0.0` 起提供下面 10 个正式浮动标签，其中包含 `debian-full`。在 Watchtower 或 Compose 部署中，浮动标签会持续收到新版，固定版本标签用于锁定环境。

| 正式浮动标签 | 固定版本标签示例 | 基础系统 | Python | 工具档位 | 支持平台 |
|--------------|------------------|----------|--------|----------|----------|
| `latest` | `3.2.7` | Alpine | 3.12 | 精简 | amd64 / arm64 / 386 / arm/v7 |
| `latest-full` | `3.2.7-full` | Alpine | 3.12 | 完整 | amd64 / arm64 / 386 / arm/v7 |
| `latest-3.10` | `3.2.7-3.10` | Alpine | 3.10 | 精简 | amd64 / arm64 |
| `latest-3.11` | `3.2.7-3.11` | Alpine | 3.11 | 精简 | amd64 / arm64 |
| `latest-all` | `3.2.7-all` | Alpine | 3.10 / 3.11 / 3.12 | 精简 | amd64 / arm64 |
| `debian` | `3.2.7-debian` | Debian | 3.12 | 精简 | amd64 / arm64 / arm/v7 |
| `debian-full` | `3.2.7-debian-full` | Debian | 3.12 | 完整 | amd64 / arm64 / arm/v7 |
| `debian-3.10` | `3.2.7-debian-3.10` | Debian | 3.10 | 精简 | amd64 / arm64 / arm/v7 |
| `debian-3.11` | `3.2.7-debian-3.11` | Debian | 3.11 | 精简 | amd64 / arm64 / arm/v7 |
| `debian-all` | `3.2.7-debian-all` | Debian | 3.10 / 3.11 / 3.12 | 精简 | amd64 / arm64 / arm/v7 |

后续版本只替换固定版本标签里的版本号，后缀保持不变。

#### Python 去重与 32 位例外

- Alpine 的 `amd64 / arm64` 和全部 Debian 镜像不再同时安装系统 Python，只保留 `/opt/daidai-python` 下的目标独立运行时。
- `latest-all` 和 `debian-all` 只包含 Python 3.10、3.11、3.12 三套独立运行时，不会再多装一套系统 Python。
- Alpine 的 `linux/386` 与 `linux/arm/v7` 没有对应的独立 Python 资产，因此 `latest`、`latest-full` 在这两个平台只使用 Alpine 系统 Python 3.12。这是 32 位平台的兼容例外，仍然只有一套 Python。
- `latest-3.10`、`latest-3.11`、`latest-all` 只发布 `amd64 / arm64`，不会发布“标签写 3.10，实际却是 3.12”的 32 位镜像。

#### 新标签与旧别名迁移

自 `v3.0.0` 起，新的连字符标签是正式名称。下面 6 个旧浮动标签仍由同一次构建推送，现有 Watchtower 部署不会因为改名而断更：

| 旧兼容别名 | 新正式标签 |
|------------|------------|
| `latest3.10` | `latest-3.10` |
| `latest3.11` | `latest-3.11` |
| `latestall` | `latest-all` |
| `debian3.10` | `debian-3.10` |
| `debian3.11` | `debian-3.11` |
| `debianall` | `debian-all` |

Debian 的旧固定版本格式也保留兼容别名：`3.2.7-debian3.10`、`3.2.7-debian3.11`、`3.2.7-debianall` 分别对应新的 `3.2.7-debian-3.10`、`3.2.7-debian-3.11`、`3.2.7-debian-all`。新部署请直接使用新名称。

#### 切换标签与本地构建

仓库里的两份基础 Compose 都只需要设置一次镜像变量。例如切换到 Alpine 完整版：

```bash
DAIDAI_PANEL_IMAGE=linzixuanzz/daidai-panel:latest-full docker compose up -d
```

也可以在 `.env` 中设置 `DAIDAI_PANEL_IMAGE=linzixuanzz/daidai-panel:latest-full` 后再运行 `docker compose up -d`。Compose 会把同一个值同时写入 `image` 和 `IMAGE_NAME`，不会出现容器运行标签和更新标签不一致。

切到 Debian 运行时：

```bash
# 仓库里有现成的 compose
docker compose -f docker-compose.debian.yml up -d

# 或基于源码本地构建
docker build --build-arg VERSION=dev -f Dockerfile.debian -t daidai-panel:debian-local .
```

本地构建时，`PYTHON_RUNTIME_MODE` 决定单版本或三版本，`PYTHON_RUNTIME_VERSION` 决定单版本镜像的 Python 版本，`INSTALL_FULL_TOOLS=true` 决定是否安装完整开发工具。下面的命令可以直接运行：

```bash
# Alpine：单版本 Python 3.10 精简版；改成 3.11 或 3.12 可构建对应单版本
docker build \
  --build-arg VERSION=dev \
  --build-arg PYTHON_RUNTIME_MODE=single \
  --build-arg PYTHON_RUNTIME_VERSION=3.10 \
  --build-arg INSTALL_FULL_TOOLS=false \
  -t daidai-panel:latest-3.10-local .

# Alpine：默认 Python 3.12 完整版
docker build \
  --build-arg VERSION=dev \
  --build-arg PYTHON_RUNTIME_MODE=single \
  --build-arg PYTHON_RUNTIME_VERSION=3.12 \
  --build-arg INSTALL_FULL_TOOLS=true \
  -t daidai-panel:latest-full-local .

# Alpine：Python 3.10 / 3.11 / 3.12 三版本精简版
docker build \
  --build-arg VERSION=dev \
  --build-arg PYTHON_RUNTIME_MODE=all \
  --build-arg PYTHON_RUNTIME_VERSION=3.12 \
  --build-arg INSTALL_FULL_TOOLS=false \
  -t daidai-panel:latest-all-local .

# Debian：单版本 Python 3.11 精简版
docker build -f Dockerfile.debian \
  --build-arg VERSION=dev \
  --build-arg PYTHON_RUNTIME_MODE=single \
  --build-arg PYTHON_RUNTIME_VERSION=3.11 \
  --build-arg INSTALL_FULL_TOOLS=false \
  -t daidai-panel:debian-3.11-local .

# Debian：默认 Python 3.12 完整版
docker build -f Dockerfile.debian \
  --build-arg VERSION=dev \
  --build-arg PYTHON_RUNTIME_MODE=single \
  --build-arg PYTHON_RUNTIME_VERSION=3.12 \
  --build-arg INSTALL_FULL_TOOLS=true \
  -t daidai-panel:debian-full-local .

# Debian：Python 3.10 / 3.11 / 3.12 三版本精简版
docker build -f Dockerfile.debian \
  --build-arg VERSION=dev \
  --build-arg PYTHON_RUNTIME_MODE=all \
  --build-arg PYTHON_RUNTIME_VERSION=3.12 \
  --build-arg INSTALL_FULL_TOOLS=false \
  -t daidai-panel:debian-all-local .
```

</details>

<details>
<summary><b>展开：Windows 单机版 —— 不装 Docker，下载 zip 解压双击 start.bat 就能跑</b></summary>

### Windows 单机版（不走 Docker）

Windows 用户可以直接下载编译好的 zip 解压运行，面板内置 Go 后端同时托管前端（无需 Nginx / Docker）。

1. 去 [GitHub Release](https://github.com/linzixuanzz/daidai-panel/releases) 下载 `daidai-windows-amd64.zip` 解压到任意目录（建议路径无空格、无中文，例如 `D:\daidai-panel`）。
2. 双击 `start.bat` 启动服务。
3. 浏览器访问 `http://localhost:5700`，首次进入创建管理员账号。

> 注意：仓库源码目录中的本地 `server/*.exe` 仅用于开发阶段临时调试，不作为可信发布产物。  
> Windows 正式发布包请始终以 GitHub Release 中 workflow 构建出的 `daidai-windows-amd64.zip` 为准。

解压后目录：

```
daidai-panel-windows-amd64/
├── daidai-server.exe     # 后端主程序（同端口同时服务前端）
├── ddp.exe               # 运维 CLI
├── web/                  # 前端静态资源（Go 通过 web_dir 直接托管）
├── config.yaml           # 端口 / 数据目录配置
├── start.bat             # 启动脚本（chcp 65001 兜底中文显示）
├── README.txt            # 详细使用说明
└── Dumb-Panel/           # 首次启动时自动创建，含数据库 / 脚本 / 日志 / 备份
```

**可选：脚本执行环境**。如需面板调度 Python / Node.js 脚本，请自行安装 Python 3.10+ 和 Node.js 20 LTS 并勾选 "Add to PATH"，重启 `start.bat` 即可（`ddp.exe`、脚本执行器会从 PATH 找到对应的 `python` / `node`）。

**Python 多版本说明**：二进制部署包不会内置 Python 3.10 / 3.11 / 3.12 三个解释器，用户只需要安装实际要使用的版本。面板会为已检测到的 Python 版本创建独立依赖环境；未安装的版本会在依赖管理里提示不可用，不影响其他版本的脚本运行。Windows 建议安装官方 Python 并保留 `py` 启动器，Linux 需要确保 `python3.10` / `python3.11` / `python3.12` 能在 PATH 中被找到。

**升级**：优先在面板后台进入「系统设置」→「概览」→「检查系统更新」→「立即更新」。二进制后台更新会自动下载对应平台的 Release 包，替换程序与前端文件，并保留现有 `config.yaml`、`Dumb-Panel\`、`data\`、`logs\`、`backups\` 等本地配置和数据目录。只有在程序目录没有写入权限、网络无法访问 GitHub Release，或后台更新失败时，才需要手动下载新版 zip 后迁移数据。

</details>

<details>
<summary><b>展开：Android Magisk 模块 —— 已 Root 的手机上直接跑，不需要 Docker、不需要 Termux</b></summary>

### Android Magisk 模块（Root 手机）

在已 Root 的 Android 设备上直接跑面板，无需 Docker、无需 Termux。模块会在安装阶段下载一份 rootfs 到 `/data/daidai`，在容器里装好 Python / Node.js / Git 等运行时，然后通过 `rurima` 进入容器启动后端，开机自启。

- **支持**：Magisk v24.0+ / KernelSU / APatch；Android 6.0+（建议 8.0+）；**仅 `arm64`**（容器运行时只有 aarch64 构建，x86_64 设备安装时会被明确拦截）
- **默认访问**：`http://127.0.0.1:5700`，后端绑定 `0.0.0.0`，局域网 / 内网穿透可直连
- **一键更新**：自 `v3.0.3` 起可在面板里在线升级（只换面板程序与前端，容器与已装依赖不动，不用重启手机）。在线升级**覆盖不到模块脚本**，所以由模块脚本实现的新能力（例如 `v3.0.4` 的「停止面板服务」）需要重刷一次 ZIP 才有；模块 `updateJson` 会推送新版 ZIP 并保留数据
- **手动停止 / 启动**：自 `v3.0.4` 起，点模块卡片的「运行 / Action」按钮即可停止面板（再点一次启动），停止状态跨重启保持；也可以在面板「设置 → 概览 → 停止面板服务」里操作
- **下载**：[GitHub Release](https://github.com/linzixuanzz/daidai-panel/releases)

两个可选版本，**装哪个都行，但只能装一个**：

| ZIP | 容器 | 什么时候选 |
|-----|------|-----------|
| `daidai-panel-magisk-vX.Y.Z.zip` | Alpine 3.18（musl） | **默认选它**。体积小、装得快，磁盘 ≥1.5 GB |
| `daidai-panel-magisk-debian-vX.Y.Z.zip` | Debian 12（glibc） | 需要跑 glibc 预编译产物时。最典型的是面板「依赖管理」里的**一键安装 Python / Node 运行时**——它下发的是 `*-unknown-linux-gnu` 与 nodejs.org 官方构建，**在 Alpine(musl) 容器里根本无法执行**（实测 0/2）。磁盘 ≥2.5 GB |

> 自 `v3.0.3` 起两个 flavor 各有各的 `updateJson`，Debian 版在管理器里点「更新」不会再被静默换成 Alpine 版。从 `v3.0.2` 或更早升上来的 Debian 用户需要先手动刷一次 v3.0.3 的 Debian ZIP，之后管理器才会走对地址。Debian 版**仍未经过真机验证**。详见 `Magisk/README.md`。

> 📱 **完整的安装 / 升级 / 卸载 / 端口配置 / 排障文档请看 → [`Magisk/README.md`](./Magisk/README.md)**

</details>

## 文档导航

上面就是最小可用的部署路径。剩下的内容默认收起，按需展开：

| 我想… | 看哪里 |
|-------|--------|
| 先看看界面长什么样，不想为此先装一遍 | [在线演示](#在线演示)，或直接打开 <https://linzixuanzz.github.io/daidai-panel/> |
| 跑 Go 任务、装需要现场编译的依赖、换 Debian 运行时、指定 Python 3.10 / 3.11 | [快速部署](#快速部署) → 「该选哪个镜像标签」 |
| pip 装包报 `Failed building wheel` / `gcc: not found` / `CMake must be installed` | [快速部署](#快速部署) → 「该选哪个镜像标签」→ 「pip 装某个包时报…」 |
| 想在面板里装 Linux 系统包（`apk` / `apt` / `dnf` / `yum` / `microdnf` / `zypper`），或直接敲命令 | 面板「依赖管理」页 →「Linux」页签 / 工具条菜单「系统命令行」 |
| 不用 Docker，在 Windows 上直接跑 | [快速部署](#快速部署) → 「Windows 单机版」 |
| 在已 Root 的安卓手机上跑 | [快速部署](#快速部署) → 「Android Magisk 模块」，完整文档见 [`Magisk/README.md`](./Magisk/README.md) |
| 改端口、配 Nginx / 宝塔 / Caddy 反代、SSE 日志流断掉 | [端口与反向代理](#端口与反向代理) |
| 升级到新版本 | [更新](#更新) |
| 忘了密码 / 用户名，或 IP 白名单把自己锁在门外 | [容器命令 `ddp`](#容器命令-ddp) |
| 在定时任务脚本里回头调面板：发通知、写回环境变量、触发别的任务 | [脚本内调用面板能力](./docs/script-api.md) |
| 定时任务到点不执行、日志抽屉说「还没有日志记录」，或想跑 Playwright | [定时任务不执行 / 没有日志](./docs/task-not-running.md) |
| 备份、迁移、想知道数据存在哪 | [数据目录](#数据目录) |
| 查 Docker 环境变量、`config.yaml` 怎么配 | [配置参考](#配置参考) |
| 看这一版改了什么 | [v3.2.7 更新日志](./docs/release-notes/v3.2.7.md) |

## 端口与反向代理

<details>
<summary><b>展开：3 个端口分别归谁管 · 只改宿主机端口怎么写 · Magisk 模块改端口 · Nginx 反代模板（SSE 必须关 proxy_buffering）</b></summary>

### 端口三兄弟

面板在容器内有 **3 个端口**，搞清它们，大多数部署问题都会消失：

| 端口 | 由谁决定 | 默认 | 要不要改 |
|------|---------|------|----------|
| **宿主机端口** | docker `-p` 左侧 | `5700` | 常改 |
| **容器内 Nginx 端口** | 环境变量 `PANEL_PORT`，`-p` 右侧应与其一致 | `5700` | 基本不改 |
| **容器内 Go 后端端口** | 环境变量 `SERVER_PORT` | `5701` | **不要改** |

```mermaid
flowchart LR
    A[浏览器<br/>http://宿主机IP:宿主机端口]
    B[宿主机端口<br/>docker -p 左侧]
    C[容器内 Nginx<br/>PANEL_PORT 默认 5700]
    D[容器内 Go API<br/>固定 5701]

    A --> B --> C
    C -->|/api/* 反代| D
```

两条经验记住就够用：

1. **Docker 部署通常只改 `-p` 左侧**，右侧保持 `5700` 即可。
2. **宿主机 Nginx / 宝塔 / Caddy 反代的目标是宿主机端口**（比如 `127.0.0.1:5700`），**别直接代理到容器内 `5701`**——SSE 会断流、鉴权会丢。

### 想改端口

**只改宿主机端口**（最常见，比如让浏览器走 8080）：

```yaml
ports:
  - "8080:5700"
```

**连容器内 Nginx 端口一起改**（只在容器内 5700 和其他服务冲突时）：`-p` 右侧必须和 `PANEL_PORT` 一致，Go 后端 `5701` 不受影响。

```bash
docker run -d --name daidai-panel \
  -p 8080:7100 \
  -e PANEL_PORT=7100 \
  ...
```

### Magisk 模块（Android Root）改端口

Magisk 模块版和 Docker 结构不一样：没有容器内 Nginx，前端 / 后端都由单个 `daidai-server` 二进制在 `PANEL_PORT` 上直接托管，**不要**直接去改 `config.yaml`——每次开机 `service.sh` 都会按 `ports.conf` 重新生成 `config.yaml`，手动改的内容会被覆盖。

改端口的唯一入口是编辑持久化目录下的 `ports.conf`：

```bash
su
vi /data/adb/daidai-panel/ports.conf
```

> 首次安装模块时会自动生成这个文件，内容带注释，直接修改对应的值即可。

里面有三个可选变量：

| 变量 | 作用 | 默认 |
|------|------|------|
| `PANEL_PORT` | 浏览器访问面板的端口（绑定 `0.0.0.0`，本机 / 局域网 / 内网穿透都能连） | `5700` |
| `SSH_PORT` | 容器内 SSH 端口（adb / Termux 登入容器调试用） | `22` |
| `EXTRA_CORS_ORIGINS` | 额外 CORS 白名单，英文逗号分隔。仅在跨域场景需要（如内网穿透公网端口与面板端口不同，或自定义域名访问） | 空 |

示例：

```ini
PANEL_PORT=6700
SSH_PORT=2222
EXTRA_CORS_ORIGINS="https://panel.example.com,https://xx.trycloudflare.com"
```

改完后重启手机，或手动执行以下命令让配置立即生效：

```bash
su -c "sh /data/adb/modules/daidai-panel/service.sh"
```

生效后在 Magisk / KernelSU / APatch 管理器里点模块卡片的「运行」按钮，可以看到当前 `PANEL_PORT` / `SSH_PORT` 的实际监听状态。完整的 Magisk 模块安装 / 升级 / 卸载文档见 [`Magisk/README.md`](./Magisk/README.md)。

### 反向代理示例

最常见是 **宿主机 Nginx → Docker 已发布端口**。面板暴露在宿主机 `5700`，反代就指向那里：

#### 宿主机 Nginx 示例（HTTPS，含 SSE 支持）

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

server {
    listen 443 ssl http2;
    server_name your-domain.com;

    ssl_certificate     /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:5700;   # 宿主机端口，不是容器内 5701

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;

        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_buffering off;                 # SSE 日志流必须关
        proxy_read_timeout 300s;
    }
}
```

如果反代本身也跑在同一 Docker 网络里，可以直接代理到 `http://daidai-panel:5700`（依然是容器内 Nginx 端口）。

**别做的事**：

- 让浏览器或反代绕过容器内 Nginx 直接访问 Go 后端 `5701`
- 把 SSE / 下载 / 鉴权接口单独绕出去
- 让 `-p` 右侧容器端口和 `PANEL_PORT` 不一致

</details>

## 更新

<details>
<summary><b>展开：面板内一键更新分别走哪条链路（Watchtower / Docker CLI / 二进制 / Magisk 模块）· Compose 手动 pull 重建怎么写</b></summary>

### 面板内一键更新（推荐）

进入「系统设置」→「概览」→ 点「检查系统更新」。系统会自动识别当前部署方式：

- **Docker 精简版**：自 `v3.0.0` 起，统一由 Watchtower 拉取并重建容器。仓库自带的两份基础 Compose 已配置内部 HTTP API，所以页面手动更新和面板的 `auto_update` 都能触发 Watchtower；Watchtower API 没有向宿主机开放端口。
  ⚠️ 这条链路要求 **Watchtower `v1.20.0` 或更新的版本**，版本偏低时点更新会失败并报 `connection refused`。Watchtower 不会自我更新，需要手动 `docker compose pull watchtower && docker compose up -d watchtower`，详见「快速部署」里的版本提示。
- **Docker 完整版**：同样推荐使用 Watchtower。早期直接把 `/var/run/docker.sock` 挂给面板、由面板调用 Docker CLI 更新的部署仍可保留原有 Socket 挂载，但这条兼容更新链只支持完整版标签。使用 `3.2.7-full`、`3.2.7-debian-full` 这类固定完整版标签触发一键更新时，面板会切换到同系列浮动标签 `latest-full` 或 `debian-full`；精简版不包含 Docker CLI。
- **二进制部署**：自动匹配 `daidai-windows-amd64.zip` 或 `daidai-linux-*.tar.gz`，后台下载、解压、替换程序和 `web/` 前端文件，更新过程会跳过 `config.yaml` 与数据目录，避免覆盖服务器本地配置。
- **Magisk 模块版**（自 `v3.0.3`）：下载对应架构的 `daidai-linux-*.tar.gz`，只替换容器内的 `daidai-server`、`ddp` 和前端目录，同时写回模块目录并同步 `module.prop` 版本号，保证重启后不回滚。容器 rootfs、apt/apk 系统包、Python venv 与已装依赖、`config.yaml`、`ports.conf` 一概不动，**不需要重启手机**。
  ⚠️ 在线升级**替换不了模块脚本**（`service.sh` / `customize.sh` / `action.sh`），升完之后是「新面板 + 旧模块外壳」，管理器里的版本号会跟着变成新版。所以由模块脚本实现的新能力需要重刷一次 ZIP 才有 —— 例如 `v3.0.4` 的「停止面板服务」，在线升级上来的用户在面板里会看到该按钮被禁用并提示当前外壳版本。只有当新面板**根本无法**在旧外壳上运行时，面板才会在检查更新阶段直接拒绝升级并要求重刷 ZIP。

### 手动更新

先在项目目录的 `.env` 中持久写入实际使用的镜像。例如 Alpine 默认镜像写入：

```dotenv
DAIDAI_PANEL_IMAGE=linzixuanzz/daidai-panel:latest
```

然后只拉取并重建面板服务，`image` 与容器内 `IMAGE_NAME` 会继续使用同一个值：

```bash
# Alpine Compose
docker compose pull daidai-panel
docker compose up -d daidai-panel

# Debian Compose
docker compose -f docker-compose.debian.yml pull daidai-panel
docker compose -f docker-compose.debian.yml up -d daidai-panel
```

也可以把 `.env` 中的 `DAIDAI_PANEL_IMAGE` 改成对应正式标签，例如 `latest-full`、`latest-3.10`、`latest-3.11`、`latest-all`、`debian-full`、`debian-3.10`、`debian-3.11` 或 `debian-all`。

本地基于源码自己构建的镜像，重新 build 即可：

```bash
docker build --build-arg VERSION=dev -f Dockerfile.debian -t daidai-panel:debian-local .
```

</details>

## 容器命令 `ddp`

<details>
<summary><b>展开：忘了密码 / 用户名、IP 白名单把自己锁在门外、备份与恢复、脚本 / 变量 / 任务 / 订阅的命令行操作</b></summary>

容器里预置了 `ddp` CLI，覆盖运维、脚本 / 变量 / 任务 / 订阅管理、账号恢复等场景。统一入口：

```bash
docker exec -it daidai-panel ddp <subcommand>
```

> 没叫 `dd` 是因为会和 Linux 自带 `dd` 命令冲突。

### 状态与自检

```bash
ddp help                 # 查看所有子命令
ddp status               # 版本、数据目录、端口、任务数、资源占用、服务状态
ddp check                # 检查配置、数据库、运行目录、运行时命令和更新托管方式
ddp logs --lines 200     # 查看 panel.log
```

### 脚本

```bash
ddp script list
ddp script cat demo.py
ddp script fetch https://example.com/test.py --path tools/test.py
```

### 环境变量

```bash
ddp env list
ddp env get JD_COOKIE
ddp env set JD_COOKIE "pt_key=xxx;pt_pin=yyy;" --group 京东
ddp env delete <id>
```

### 任务与订阅

```bash
ddp task list --status running
ddp task logs 12 --lines 80
ddp task run 12                 # 同步执行任务并实时输出
ddp task stop 12                # 终止运行中的任务

ddp sub list
ddp sub logs 3 --lines 100
ddp sub pull 我的订阅            # 立即执行一次订阅拉取
```

### 运维

```bash
ddp restart                     # 重启容器内 daidai-server 进程
ddp update                      # 复用面板一键更新链路
ddp clean-logs 7                # 清理 7 天前的任务日志文件
ddp backup create --name nightly
ddp backup list
ddp backup restore <name>
ddp backup delete <name>
```

### 账号恢复（忘了密码 / 用户名）

```bash
ddp list-users                              # 忘了用户名先看这个
ddp reset-password admin NewPass123         # 单用户时可省略用户名
ddp reset-username admin newadmin
ddp disable-2fa admin                       # 传 --all 则全员禁用
ddp reset-login --all                       # 清登录失败次数，解锁被锁账号
ddp ip-whitelist list                       # 查看当前 IP 白名单
ddp ip-whitelist clear                      # IP 白名单填错进不去面板时，清空后恢复所有 IP 可访问
ddp ip-whitelist set 203.0.113.10           # 直接重设白名单，也支持 CIDR / IPv4 通配格式
```

> **忘记密码怎么办**：`docker exec -it daidai-panel ddp list-users` 查出用户名，再 `ddp reset-password <用户名> <新密码>`，不需要删数据重装。
> **IP 白名单填错怎么办**：进入容器执行 `docker exec -it daidai-panel ddp ip-whitelist clear`，清空后登录页会恢复所有 IP 可访问，再回面板重新添加正确白名单。

命令也支持直接跑完就退出的一次性形态：

```bash
docker run --rm \
  -v $(pwd)/Dumb-Panel:/app/Dumb-Panel \
  linzixuanzz/daidai-panel:latest \
  ddp version
```

</details>

## 数据目录

<details>
<summary><b>展开：Dumb-Panel 目录里都有什么 —— 备份、迁移带走这一个目录就够了</b></summary>

默认挂在 `./Dumb-Panel`，保留这一个目录 = 保留整个面板状态：

```
Dumb-Panel/
├── daidai.db          # SQLite 数据库
├── .jwt_secret        # 自动生成的 JWT 密钥
├── panel.log          # 面板运行日志
├── deps/              # Python / Node.js 依赖
├── scripts/           # 脚本文件
├── logs/              # 任务执行日志
└── backups/           # 数据备份
```

</details>

## 配置参考

<details>
<summary><b>展开：Docker 环境变量完整清单 · 启动配置与运行期配置的区别（改哪个、重启会不会丢）</b></summary>

面板有两层配置：

- **启动配置**：Docker 环境变量 + `config.yaml`。Docker 部署时由 `entrypoint.sh` 自动生成，一般不需要手动改。
- **运行期配置**：进面板后「系统设置」里改，落到 SQLite 的 `system_configs` 表，重启不丢失。

### Docker 环境变量

| 变量 | 说明 | 默认 |
|------|------|------|
| `TZ` | 时区 | `Asia/Shanghai` |
| `DATA_DIR` | 数据目录 | `/app/Dumb-Panel` |
| `DB_PATH` | 数据库路径 | `${DATA_DIR}/daidai.db` |
| `PANEL_PORT` | 容器内 Nginx 端口 | `5700` |
| `SERVER_PORT` | 容器内 Go 后端端口（**不要改**） | `5701` |
| `CONTAINER_NAME` / `IMAGE_NAME` | 面板内一键更新识别自己用 | 空 |
| `PANEL_UPDATE_MANAGER` | Docker 更新管理方式；精简镜像应设为 `watchtower` | 空 |
| `WATCHTOWER_HTTP_API_URL` | 面板触发 Watchtower 更新的容器内部地址，不需要映射到宿主机 | `http://watchtower:8080`（基础 Compose 的稳定服务名） |
| `WATCHTOWER_HTTP_API_TOKEN` | 面板与 Watchtower 共用的 HTTP API 令牌 | 基础 Compose 提供内部默认值，正式环境建议自定义 |
| `WATCHTOWER_HTTP_API_PERIODIC_POLLS` | 是否保留 Watchtower 定时轮询 | `true`（基础 Compose） |
| `CORS_ORIGINS` | 额外放行的跨域来源，英文逗号分隔。私有 / 局域网 IP 已自动放行，用公网域名或反代域名访问时才需要 | 空 |
| `PUID` / `PGID` | 让容器以宿主机用户身份运行（NAS 常用，SMB/NFS 共享下文件属主才对得上）。不设则以 root 运行，与历史行为一致 | 空 |

**关于 `PUID` / `PGID`（NAS 用户看这里）**

- **两个都要设**：只设 `PGID` 时 `PUID` 会取到 0，等于没降权，容器会打印说明并继续以 root 跑。宿主机执行 `id` 查看自己的真实取值。
- **取值与镜像里已有账号撞车不要紧**：Debian 版镜像基于 `node:20-bookworm-slim`，自带一个 uid/gid 都是 1000 的 `node` 用户，而 `PUID=1000` 恰好是最常见的取值 —— 容器会直接复用那个账号（`v3.0.7` 起；更早的版本在这里会直接起不来）。
- **改完 `PUID` 要重建容器**（`docker compose up -d --force-recreate`）比只 `docker restart` 更保险。
- **已知限制**：降权之后，面板里的「Linux 系统依赖」（`apt-get` / `apk`）装不了 —— 系统包管理器需要 root。面板会给出明确说明而不是报一串 `Permission denied`。**Node.js / Python 依赖不受影响**，降权下照常安装。

`DAIDAI_PANEL_IMAGE` 是宿主机上的 Compose 变量，不是容器内变量。两份基础 Compose 都用它同时设置 `image` 和 `IMAGE_NAME`。

</details>

## 技术栈

<details>
<summary><b>展开：前端 / 后端 / 部署分别用了什么</b></summary>

| 层 | 技术 |
|----|------|
| 前端 | Vue 3 + TypeScript + Element Plus + Pinia + Vite + CodeMirror 6（默认引擎）/ Monaco（桌面端可选，按需加载） |
| 后端 | Go 1.25 + Gin + GORM + SQLite（`glebarez/sqlite` 纯 Go port，`CGO_ENABLED=0`） |
| 部署 | Nginx + Go Binary，Docker 多架构镜像：`linux/amd64` / `linux/arm64` / `linux/386` / `linux/arm/v7` |

</details>

## 致谢

<details>
<summary><b>展开：本项目参考与借鉴的开源项目</b></summary>

本项目的开发离不开以下优秀的开源项目：

- **[白虎面板 (Baihu Panel)](https://github.com/engigu/baihu-panel)** — 后端框架架构参考，部分代码基于白虎面板改进
- **[青龙面板 (Qinglong)](https://github.com/whyour/qinglong)** — 功能设计参考，定时任务管理、环境变量、订阅管理等核心功能借鉴自青龙面板

感谢以上项目作者的贡献！

</details>

## LICENSE

Copyright © 2026, [linzixuanzz](https://github.com/linzixuanzz). Released under the [MIT](LICENSE).
