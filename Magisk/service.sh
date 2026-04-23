#!/system/bin/sh
##########################################################################
# 呆呆面板 Magisk 模块 - late_start service
#
# 进入 Alpine 容器启动 daidai-server（端口可通过 ports.conf 配置）。
# 前端静态资源由 daidai-server 直接托管，不再依赖 nginx。
##########################################################################

export PATH=/data/adb/ap/bin:/data/adb/ksu/bin:/data/adb/magisk:$PATH

# rootfs 位置探测
rootfs=/data/daidai
if [ ! -d "$rootfs" ]; then
  rootfs=/data/local/daidai
fi

# 模块目录探测
MODDIR=${MODDIR:-/data/adb/modules/daidai-panel}
[ ! -d "$MODDIR" ] && MODDIR=/data/adb/magisk/modules/daidai-panel
[ ! -d "$MODDIR" ] && MODDIR=/sbin/.magisk/modules/daidai-panel
[ ! -d "$MODDIR" ] && MODDIR=$(dirname "$0")
RURIMA=$MODDIR/system/bin/rurima

PERSIST_DIR=/data/adb/daidai-panel
LOG_FILE="$PERSIST_DIR/service.log"
PORTS_CONF="$PERSIST_DIR/ports.conf"

mkdir -p "$PERSIST_DIR"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG_FILE" 2>/dev/null
}

# 日志滚动
if [ -f "$LOG_FILE" ]; then
  size=$(stat -c%s "$LOG_FILE" 2>/dev/null || echo 0)
  [ "${size:-0}" -gt 2097152 ] && mv -f "$LOG_FILE" "$LOG_FILE.old" 2>/dev/null
fi

# ---- 端口配置（用户可编辑 ports.conf 自定义） ---------------------------
# 第一次运行时若文件缺失，自动补一份默认值
if [ ! -f "$PORTS_CONF" ]; then
  cat > "$PORTS_CONF" << 'PCONF'
# 呆呆面板端口配置 —— 修改后重启模块生效
PANEL_PORT=5700
SSH_PORT=22
SSH_USER=root
SSH_PASSWORD=123456
PCONF
fi

PANEL_PORT=5700
SSH_PORT=22
SSH_USER=root
SSH_PASSWORD=123456
EXTRA_CORS_ORIGINS=""
# shellcheck disable=SC1090
. "$PORTS_CONF" 2>/dev/null || true

# 合法性校验（必须是 1..65535 之间的整数）
validate_port() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}
if ! validate_port "$PANEL_PORT"; then
  log "!! ports.conf 中 PANEL_PORT='$PANEL_PORT' 非法，回退为 5700"
  PANEL_PORT=5700
fi
if ! validate_port "$SSH_PORT"; then
  log "!! ports.conf 中 SSH_PORT='$SSH_PORT' 非法，回退为 22"
  SSH_PORT=22
fi

log "========================================="
log "呆呆面板模块启动 (MODDIR=$MODDIR, rootfs=$rootfs)"
log "端口: PANEL_PORT=$PANEL_PORT (绑定 0.0.0.0), SSH_PORT=$SSH_PORT (来源: $PORTS_CONF)"
log "SSH 凭据: 用户=$SSH_USER"
if [ -n "$EXTRA_CORS_ORIGINS" ]; then
  log "额外 CORS 来源: $EXTRA_CORS_ORIGINS"
fi
log "========================================="

echo "noSuspend" > /sys/power/wake_lock 2>/dev/null
dumpsys deviceidle disable 2>/dev/null || true

# 等网络就绪（尽量，失败也不阻塞）
for i in 1 2 3 4 5; do
  if busybox nslookup m.baidu.com >/dev/null 2>&1; then
    log "网络已就绪"
    break
  fi
  sleep 5
done

if [ ! -f "$RURIMA" ]; then
  log "!! 找不到 rurima 二进制: $RURIMA"
  exit 1
fi

chmod +x "$RURIMA" 2>/dev/null

if [ ! -d "$rootfs" ]; then
  log "!! 找不到 rootfs: $rootfs，模块可能未完成安装，请重装"
  exit 1
fi

# KernelSU 下 /data 可能以 ro 挂载，确保可写
if [ -d "/data/adb/ksu" ]; then
  mount -o remount,rw /data 2>/dev/null
fi

# 把最新的前端和 daidai-server 同步进容器
mkdir -p $rootfs/app/web $rootfs/app/Dumb-Panel $rootfs/usr/local/bin
cp -rf $MODDIR/web/* $rootfs/app/web/ 2>/dev/null
cp -f  $MODDIR/system/bin/daidai-server $rootfs/usr/local/bin/daidai-server 2>/dev/null
chmod 755 $rootfs/usr/local/bin/daidai-server 2>/dev/null

if [ -f $MODDIR/system/bin/ddp ]; then
  cp -f  $MODDIR/system/bin/ddp $rootfs/usr/local/bin/ddp 2>/dev/null
  chmod 755 $rootfs/usr/local/bin/ddp 2>/dev/null
fi

cp -f $MODDIR/module.prop $rootfs/app/module.prop 2>/dev/null

# 把持久化的 ports.conf 同步进容器，容器启动脚本直接 source
mkdir -p $rootfs/tmp
cp -f "$PORTS_CONF" "$rootfs/tmp/ports.conf" 2>/dev/null

# ---- 生成容器启动脚本（全字面 heredoc，变量由容器内 . /tmp/ports.conf 注入） ----
STARTUP=$rootfs/tmp/daidai-startup.sh

cat > "$STARTUP" << 'CONTAINER_EOF'
#!/bin/ash
# 默认值 + 用户 ports.conf 覆盖（同文件已由宿主 service.sh 校验过合法性）
PANEL_PORT=5700
SSH_PORT=22
SSH_USER=root
SSH_PASSWORD=123456
EXTRA_CORS_ORIGINS=""
[ -f /tmp/ports.conf ] && . /tmp/ports.conf

export DAIDAI_DIR=/app/Dumb-Panel
export LANG=C.UTF-8
export HOME=/root
export SHELL=/bin/bash
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/app
export NODE_PATH=/usr/local/lib/node_modules

mkdir -p $DAIDAI_DIR/scripts $DAIDAI_DIR/logs $DAIDAI_DIR/deps/nodejs $DAIDAI_DIR/deps/python $DAIDAI_DIR/backups
chmod 777 $DAIDAI_DIR

# Python 虚拟环境（第一次进入时创建）
if [ ! -d "$DAIDAI_DIR/deps/python/venv" ]; then
  python3 -m venv $DAIDAI_DIR/deps/python/venv 2>/dev/null || true
fi

# 按配置写入 config.yaml（每次启动都覆盖，保证端口与 ports.conf 一致）
# 后端用 net.Listen(":PORT") 绑定 0.0.0.0，穿透/局域网直连均可；
# CORS 列表只影响浏览器跨域检查，"同源请求"已由中间件自动放行。
cat > $DAIDAI_DIR/config.yaml << YAML
server:
  port: ${PANEL_PORT}
  mode: release
  web_dir: /app/web

database:
  path: /app/Dumb-Panel/daidai.db

jwt:
  secret: ""
  access_token_expire: 480h
  refresh_token_expire: 1440h

data:
  dir: /app/Dumb-Panel
  scripts_dir: /app/Dumb-Panel/scripts
  log_dir: /app/Dumb-Panel/logs

cors:
  origins:
    - http://localhost:${PANEL_PORT}
    - http://127.0.0.1:${PANEL_PORT}
YAML

# 追加 EXTRA_CORS_ORIGINS（穿透 / 反代 / 公网域名场景显式放行）
if [ -n "${EXTRA_CORS_ORIGINS}" ]; then
  echo "${EXTRA_CORS_ORIGINS}" | tr ',;' '\n' | while IFS= read -r origin; do
    # 去首尾空白
    origin=$(echo "$origin" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [ -z "$origin" ] && continue
    echo "    - ${origin}" >> $DAIDAI_DIR/config.yaml
  done
fi

# ---- SSH: 同步用户名/密码，按 SSH_PORT 更新 sshd_config 并启动 --------
# 每次启动都同步密码，确保 ports.conf 改了密码后重启即生效
if [ -n "${SSH_USER}" ] && [ -n "${SSH_PASSWORD}" ]; then
  if [ "${SSH_USER}" != "root" ]; then
    id "${SSH_USER}" >/dev/null 2>&1 || adduser -D -s /bin/bash "${SSH_USER}" 2>/dev/null
  fi
  echo "${SSH_USER}:${SSH_PASSWORD}" | chpasswd 2>/dev/null
fi

if [ -f /etc/ssh/sshd_config ]; then
  # 清除已有 Port 行（包括注释的），再追加当前端口
  sed -i -E '/^[#[:space:]]*Port[[:space:]]+/d' /etc/ssh/sshd_config
  echo "Port ${SSH_PORT}" >> /etc/ssh/sshd_config
  # 没有 host key 的话先生成一下
  [ -f /etc/ssh/ssh_host_rsa_key ] || ssh-keygen -A >/dev/null 2>&1
  # 启动 sshd（已在跑就跳过）
  if ! pgrep -x sshd >/dev/null 2>&1; then
    mkdir -p /run/sshd
    /usr/sbin/sshd >/dev/null 2>&1 || true
  fi
fi

# 避免重复拉起 daidai-server
if pgrep -f /usr/local/bin/daidai-server >/dev/null 2>&1; then
  echo "daidai-server 已在运行" >> $DAIDAI_DIR/service.log
  exit 0
fi

cd $DAIDAI_DIR
nohup /usr/local/bin/daidai-server > $DAIDAI_DIR/daidai.log 2>&1 &
echo "daidai-server 已拉起 PID=$! (port=${PANEL_PORT})" >> $DAIDAI_DIR/service.log
exit 0
CONTAINER_EOF
chmod +x "$STARTUP" 2>/dev/null

log "进入 Alpine 容器启动 daidai-server (panel=$PANEL_PORT, ssh=$SSH_PORT)..."

"$RURIMA" ruri -p -N -S -A $rootfs /bin/ash /tmp/daidai-startup.sh

sleep 2

# 容器内启动后简单验证
if "$RURIMA" ruri -p -N -S -A $rootfs /bin/ash -c "pgrep -f /usr/local/bin/daidai-server >/dev/null 2>&1"; then
  log "面板启动成功，访问 http://127.0.0.1:${PANEL_PORT}"
else
  log "!! 面板启动失败，查看 $rootfs/app/Dumb-Panel/daidai.log"
fi
