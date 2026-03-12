<h1 align="center">
  <br>
  呆呆面板 (Daidai Panel)
  <br>
  轻量级定时任务管理面板
</h1>

<p align="center">
  Python · Node.js · Shell · TypeScript
</p>

## 介绍

呆呆面板是一个轻量级的定时任务管理平台，类似青龙面板，支持多语言脚本的定时执行与可视化管理，适用于个人和中小型团队的自动化运维场景。

项目采用前后端分离架构，前端使用 React + TypeScript + Ant Design，后端使用 Python Flask + SQLAlchemy + APScheduler，部署采用 Nginx + Gunicorn 单镜像方案，开箱即用。

## 功能特性

- **定时任务** — Cron 表达式调度，支持重试、超时、任务依赖、前后置钩子
- **脚本管理** — 在线编辑/上传，支持 Python、Node.js、Shell、TypeScript 等
- **执行日志** — 实时日志流（SSE/WebSocket），历史日志查看与自动清理
- **环境变量** — 分组管理、拖拽排序、批量导入导出（兼容青龙格式）
- **订阅管理** — 自动从 Git 仓库拉取脚本，支持定期同步
- **依赖管理** — 可视化安装/卸载 Node.js 和 Python 依赖
- **通知推送** — 支持 Bark、Telegram、Server酱、企业微信等
- **开放 API** — OAuth 风格的 API 管理，支持外部系统调用
- **系统安全** — 双因素认证 (2FA)、IP 白名单、登录日志、会话管理
- **数据备份** — 加密备份与恢复，一键导出数据库和脚本
- **系统监控** — 实时 CPU/内存/磁盘监控，执行趋势统计

## 安装方法

### Docker Compose（推荐）

```yaml
version: '3.8'
services:
  daidai-panel:
    image: linzixuanzz/daidai-panel:latest
    container_name: daidai-panel
    restart: unless-stopped
    ports:
      - "5700:7100"
    volumes:
      - ./data:/dd/data
      - ./scripts:/dd/scripts
      - ./log:/dd/log
      - ./config:/dd/config
    environment:
      - TZ=Asia/Shanghai
```

```bash
docker-compose up -d
```

### Docker Run

```bash
docker run -d \
  --name daidai-panel \
  --restart unless-stopped \
  -p 5700:7100 \
  -v $(pwd)/data:/dd/data \
  -v $(pwd)/scripts:/dd/scripts \
  -v $(pwd)/log:/dd/log \
  -v $(pwd)/config:/dd/config \
  -e TZ=Asia/Shanghai \
  linzixuanzz/daidai-panel:latest
```

之后访问 `http://localhost:5700` 进入管理面板，首次使用需要初始化管理员账号。

## 更新方法

```bash
docker pull linzixuanzz/daidai-panel:latest
docker compose up -d
```

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `TZ` | 时区 | `Asia/Shanghai` |
| `GUNICORN_WORKERS` | 工作进程数 | `2` |
| `GUNICORN_THREADS` | 每进程线程数 | `4` |
| `ADMIN_USERNAME` | 初始管理员用户名 | `admin` |
| `ADMIN_PASSWORD` | 初始管理员密码 | `admin123` |

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React 18 + TypeScript + Ant Design 5 + Vite |
| 后端 | Python 3.11 + Flask 3 + SQLAlchemy + APScheduler |
| 部署 | Nginx + Gunicorn，Docker 单镜像 |

## LICENSE

Copyright © 2026, [linzixuanzz](https://github.com/linzixuanzz). Released under the [MIT](LICENSE).
