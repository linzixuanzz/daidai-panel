# Linux 二进制部署（含飞牛 OS / 群晖 / 绿联等 NAS）

本目录提供 systemd 部署模板，专门处理国产 NAS 系统上常见的几类坑。

## 安装步骤

1. 下载二进制 `daidai-server` 与前端静态资源到 `/opt/daidai-panel`。
2. 复制 systemd 单元：
   ```bash
   sudo cp daidai-panel.service /etc/systemd/system/daidai-panel.service
   ```
3. （推荐）创建专用用户：
   ```bash
   sudo useradd -r -s /sbin/nologin -d /opt/daidai-panel daidai
   sudo chown -R daidai:daidai /opt/daidai-panel
   ```
   然后取消 `daidai-panel.service` 中 `User=daidai` 和 `Group=daidai` 的注释。
4. 启动：
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now daidai-panel
   sudo systemctl status daidai-panel
   ```

## 飞牛 OS / 群晖等 NAS 注意事项

### 1. pip 报 "Cannot set --home and --prefix together"

部分 NAS 在 `/etc/environment` 或 systemd 全局配置里预设了 `PIP_HOME` / `PIP_PREFIX` 等变量，面板进程继承后调用 `pip install` 会冲突报错。

modeled service 文件已经通过 `Environment=PIP_PREFIX=` 等空赋值清理掉这些变量，无需额外处理。如果你用别的方式拉起面板（裸跑、自定义脚本），务必在启动前 `unset PIP_PREFIX PIP_HOME PIP_TARGET PIP_ROOT PIP_USER PIP_INSTALL_OPTION PYTHONUSERBASE`。

### 2. 登录报 403

浏览器从 NAS 域名访问、面板在内部端口跑时，CORS 预检会被拒。代码已对私有/局域网 IP 自动放行；若你用公网域名访问，请在 `config.yaml` 加：
```yaml
cors:
  origins:
    - https://panel.your-domain.com
```
然后 `sudo systemctl restart daidai-panel`。

### 3. 反向代理后 IP 显示为本地

可信代理在面板里配置：「系统设置 → 代理设置 → 可信代理 CIDR」（没有对应的环境变量）。v3.4.4 起默认只信任本机（127.0.0.1 / ::1），如果面板放在 nginx、NAS 反代等之后，请把反代的来源地址（IP 或 CIDR）加进去，否则登录日志、限流等看到的都是反代的地址。

### 4. SQLite 写入失败

确保 `WorkingDirectory` 及其 `data/` 子目录对运行用户可写。如果用 `User=daidai`：
```bash
sudo chown -R daidai:daidai /opt/daidai-panel
```

### 5. SSE 长连接（任务实时日志）

如果你在面板前面再加了一层 nginx 反代，请配置：
```nginx
proxy_http_version 1.1;
proxy_set_header Connection "";
proxy_buffering off;
proxy_read_timeout 24h;
add_header X-Accel-Buffering no;
```

## Docker 替代方案

更省心的方式是用 Docker 跑（仓库根目录提供 `docker-compose.yml`）。Docker 镜像里已经处理好上述所有问题，并且支持 `PUID`/`PGID` 环境变量适配 NAS 用户。
