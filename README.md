<p align="center">
  <img src="./图标.png" alt="呆呆面板" width="120">
</p>

<h1 align="center">呆呆面板</h1>

<p align="center">
  <em>轻量、现代的定时任务管理面板，Docker 一键部署，开箱即用</em>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Vue-3-4FC08D?logo=vue.js&logoColor=white" alt="Vue3">
  <img src="https://img.shields.io/badge/Element%20Plus-2.x-409EFF?logo=element&logoColor=white" alt="Element Plus">
  <img src="https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white" alt="PostgreSQL">
  <img src="https://img.shields.io/badge/Docker-Ready-2496ED?logo=docker&logoColor=white" alt="Docker">
</p>

---

呆呆面板 (Daidai Panel) 是一款轻量级定时任务管理平台，采用 Go (Gin) + Vue3 (Element Plus) + PostgreSQL 架构，专注于脚本托管与自动化任务调度。支持 Python、Node.js、Shell 等多语言脚本的定时执行与可视化管理，内置 18 种消息推送渠道、订阅管理、环境变量、依赖管理、Open API 等功能。Docker 一键部署，开箱即用。

## 功能特性

- **定时任务** — Cron 表达式调度，支持重试、超时、任务依赖、前后置钩子
- **脚本管理** — 在线代码编辑器，支持 Python、Node.js、Shell、TypeScript
- **执行日志** — SSE 实时日志流，历史日志查看与自动清理
- **环境变量** — 分组管理、拖拽排序、批量导入导出（兼容青龙格式）
- **订阅管理** — 自动从 Git 仓库拉取脚本，支持定期同步
- **依赖管理** — 可视化安装/卸载 Python (pip) 和 Node.js (npm) 依赖
- **通知推送** — Bark、Telegram、Server酱、企业微信、钉钉、飞书等 18 种渠道
- **开放 API** — App Key / App Secret 认证，支持第三方系统对接
- **系统安全** — 双因素认证 (2FA)、IP 白名单、登录日志、会话管理
- **数据备份** — 一键备份与恢复，导出全部数据
- **系统监控** — 实时 CPU / 内存 / 磁盘监控，任务执行趋势统计

<details>
<summary><b>点击展开查看详细功能</b></summary>

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
- 支持文件上传
- 脚本版本管理

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
- 白名单/黑名单过滤

### 消息推送
- 18 种主流推送渠道
- 任务执行结果通知
- 系统事件告警
- 自定义推送模板

### 系统设置
- 双因素认证 (2FA / TOTP)
- IP 白名单
- 登录日志与会话管理
- 数据备份与恢复

</details>

## 效果图

<details>
<summary><b>点击展开查看界面截图</b></summary>

| 功能 | 截图 |
|------|------|
| 登录页面 | ![登录](./image.png) |
| 仪表盘 | ![仪表盘](./image%20copy.png) |
| 定时任务 | ![定时任务](./image%20copy%202.png) |
| 脚本管理 | ![脚本管理](./image%20copy%203.png) |
| 环境变量 | ![环境变量](./image%20copy%204.png) |
| 订阅管理 | ![订阅管理](./image%20copy%205.png) |
| 消息通知 | ![消息通知](./image%20copy%206.png) |
| 依赖管理 | ![依赖管理](./image%20copy%207.png) |
| API 文档 | ![API文档](./image%20copy%208.png) |

</details>

## 快速部署

### Docker Compose（推荐）

```yaml
services:
  daidai-panel:
    image: linzixuanzz/daidai-panel:latest
    container_name: daidai-panel
    restart: unless-stopped
    ports:
      - "5700:5700"
    volumes:
      - ./data:/app/data
      - /var/run/docker.sock:/var/run/docker.sock  # 支持面板内一键更新
    environment:
      - TZ=Asia/Shanghai
      - CONTAINER_NAME=daidai-panel
      - IMAGE_NAME=linzixuanzz/daidai-panel:latest
```

```bash
docker compose up -d
```

### Docker Run

```bash
docker run -d \
  --name daidai-panel \
  --restart unless-stopped \
  -p 5700:5700 \
  -v $(pwd)/data:/app/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e TZ=Asia/Shanghai \
  -e CONTAINER_NAME=daidai-panel \
  -e IMAGE_NAME=linzixuanzz/daidai-panel:latest \
  linzixuanzz/daidai-panel:latest
```

启动后访问：`http://localhost:5700`

首次使用需要初始化管理员账号。

> **说明**：挂载 `/var/run/docker.sock` 是为了支持面板内一键更新功能。如果不需要此功能，可以移除该挂载。

## 多架构支持

镜像同时支持 `linux/amd64` 和 `linux/arm64`，可在 x86 服务器和 ARM 设备（如树莓派、Oracle ARM 云服务器）上直接运行。

## 更新方法

### 方式一：面板内一键更新（推荐）

进入「系统设置」→「概览」，点击「检查系统更新」，如有新版本会提示一键更新。

### 方式二：手动更新

```bash
docker pull linzixuanzz/daidai-panel:latest
docker compose up -d
```

## 数据目录

```
./data/
├── daidai.db          # PostgreSQL 数据（容器内置）
├── scripts/           # 脚本文件存储
├── logs/              # 执行日志
└── backups/           # 数据备份
```

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | Vue 3 + TypeScript + Element Plus + Pinia + Vite |
| 后端 | Go (Gin) + GORM + PostgreSQL |
| 部署 | Nginx + Go Binary，Docker 单镜像（AMD64 / ARM64） |

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `TZ` | 时区 | `Asia/Shanghai` |
| `DB_PATH` | 数据库路径 | `./data/daidai.db` |
| `SERVER_PORT` | Go 服务端口 | `5701` |

<details>
<summary><b>Nginx 反向代理配置（HTTPS）</b></summary>

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
        proxy_pass http://127.0.0.1:5700;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;

        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_buffering off;
        proxy_read_timeout 300s;
    }
}
```

</details>

## LICENSE

Copyright © 2026, [linzixuanzz](https://github.com/linzixuanzz). Released under the [MIT](LICENSE).
