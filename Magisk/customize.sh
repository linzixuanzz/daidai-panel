#!/system/bin/sh
##########################################################################
# 呆呆面板 Magisk / KernelSU / APatch 模块安装脚本
#
# 方案：借鉴 v2.0.5 的容器方案
#   1. 释放 rurima (静态 arm64) 到 /system/bin （由 Magisk 魔挂）
#   2. 下载 Alpine minirootfs 解压到 rootfs 目录
#   3. 通过 rurima ruri 进入 Alpine，用 apk 安装 python3 / nodejs / npm / git / curl / bash 等
#   4. 面板后端 daidai-server (CGO_ENABLED=0 静态 Go 二进制) 放进容器 /usr/local/bin/
#   5. 运行时由 service.sh 通过 rurima ruri 进入容器启动 daidai-server，
#      单端口 5700 由 daidai-server 直接托管 API + 前端静态文件 (web_dir)
##########################################################################

SKIPUNZIP=0
REPLACE=""

# ---- 基础变量 ------------------------------------------------------------
export PATH=/data/adb/ap/bin:/data/adb/ksu/bin:/data/adb/magisk:$PATH:$MODPATH/system/bin

# rootfs 优先使用 /data/daidai（若历史已存在），否则 /data/local/daidai
export rootfs=/data/local/daidai
if [ -d "/data/daidai" ]; then
  export rootfs=/data/daidai
fi

MODID=daidai-panel
PERSIST_DIR=/data/adb/$MODID
UPDATE_FLAG="$PERSIST_DIR/.updated_from"

# ---- 环境探测 ------------------------------------------------------------
detect_ksu() { [ -d "/data/adb/ksu" ]; }

get_current_version() {
  # 已启用模块的 module.prop —— 按 Magisk / KernelSU / APatch 常见路径依次查找
  for candidate in \
    "/data/adb/modules/$MODID/module.prop" \
    "/data/adb/ksu/modules/$MODID/module.prop" \
    "/data/adb/ap/modules/$MODID/module.prop" \
    "$PERSIST_DIR/module.prop"
  do
    if [ -f "$candidate" ]; then
      grep '^versionCode=' "$candidate" 2>/dev/null | cut -d'=' -f2
      return
    fi
  done
  echo "0"
}

# ---- 架构检查 ------------------------------------------------------------
if [ "$ARCH" != "arm64" ] && [ "$ARCH" != "x64" ]; then
  abort "! 当前仅支持 arm64 / x86_64，设备架构 $ARCH 暂不支持"
fi

if [ "$API" -lt 26 ]; then
  abort "! 要求 Android 8.0 (API 26) 及以上，当前 API=$API"
fi

# ---- 根据架构挑选 daidai-server 二进制 ----------------------------------
if [ "$ARCH" = "arm64" ]; then
  BIN_SUFFIX="arm64"
else
  BIN_SUFFIX="amd64"
fi

if [ ! -f "$MODPATH/system/bin/daidai-server-${BIN_SUFFIX}" ]; then
  abort "! 模块包缺少 system/bin/daidai-server-${BIN_SUFFIX}，无法安装"
fi

mv -f "$MODPATH/system/bin/daidai-server-${BIN_SUFFIX}" "$MODPATH/system/bin/daidai-server"
[ -f "$MODPATH/system/bin/daidai-server-arm64" ] && rm -f "$MODPATH/system/bin/daidai-server-arm64"
[ -f "$MODPATH/system/bin/daidai-server-amd64" ] && rm -f "$MODPATH/system/bin/daidai-server-amd64"

# ddp CLI（如果有）
if [ -f "$MODPATH/system/bin/ddp-${BIN_SUFFIX}" ]; then
  mv -f "$MODPATH/system/bin/ddp-${BIN_SUFFIX}" "$MODPATH/system/bin/ddp"
fi
[ -f "$MODPATH/system/bin/ddp-arm64" ] && rm -f "$MODPATH/system/bin/ddp-arm64"
[ -f "$MODPATH/system/bin/ddp-amd64" ] && rm -f "$MODPATH/system/bin/ddp-amd64"

set_perm_recursive $MODPATH/system/bin 0 2000 0755 0755

# ---- 打印安装信息 -------------------------------------------------------
if detect_ksu; then
  ui_print "- 检测到 KernelSU 环境"
else
  ui_print "- 检测到 Magisk 环境"
fi

ui_print ""
ui_print "------------呆呆面板安装环境----------"
ui_print "设备：$(getprop ro.product.model)"
ui_print "系统版本：$(getprop ro.build.version.release)"
ui_print "安卓版本：$(getprop ro.build.version.sdk)"
if [ -f "/data/adb/ksu/kernel/version" ]; then
  ui_print "KernelSU版本：$(cat /data/adb/ksu/kernel/version)"
else
  ui_print "Magisk版本：$(cat /data/adb/magisk/version 2>/dev/null || echo 'N/A')"
fi
ui_print "-------------------------------------"
ui_print ""

# ---- 版本升级时保留用户数据 ---------------------------------------------
current_ver=$(get_current_version)
new_ver=$(grep '^versionCode=' $MODPATH/module.prop 2>/dev/null | cut -d'=' -f2)

if [ "$current_ver" != "0" ] && [ "$current_ver" -lt "$new_ver" ] 2>/dev/null; then
  ui_print "- 检测到版本更新: $current_ver -> $new_ver"
  ui_print "- 正在保留用户数据..."
  if [ -d "$rootfs/app/Dumb-Panel" ]; then
    mkdir -p "$TMPDIR/backup_data" || abort "! 无法创建数据备份目录 $TMPDIR/backup_data"
    if ! cp -rf "$rootfs/app/Dumb-Panel/." "$TMPDIR/backup_data/" 2>/dev/null; then
      abort "! 用户数据备份失败（$TMPDIR 空间可能不足），已中止升级以保护数据"
    fi
    ui_print "- 数据已备份到 $TMPDIR/backup_data"
  fi
  mkdir -p "$PERSIST_DIR"
  echo "$current_ver" > "$UPDATE_FLAG"
fi

# 极少数情况下 /data 挂载异常，提示用户重启后重试
if [ -e "$rootfs/sys/kernel" ] && [ "$current_ver" = "0" ]; then
  abort "- 请重启后再尝试安装！"
fi

# ---- 清掉旧 rootfs 重装 -------------------------------------------------
rm -rf $rootfs

ui_print "- 请勿切换到后台，避免下载失败！"
ui_print "- 正在联网下载 Alpine rootfs..."

ALPINE_URL="https://mirrors.nju.edu.cn/alpine/v3.18/releases/aarch64/alpine-minirootfs-3.18.9-aarch64.tar.gz"
if [ "$ARCH" = "x64" ]; then
  ALPINE_URL="https://mirrors.nju.edu.cn/alpine/v3.18/releases/x86_64/alpine-minirootfs-3.18.9-x86_64.tar.gz"
fi

busybox wget --no-check-certificate -O $TMPDIR/rootfs.tar.gz "$ALPINE_URL" || \
  abort "! Alpine rootfs 下载失败，请检查网络后重试"

mkdir -p $rootfs
tar -xf $TMPDIR/rootfs.tar.gz -C $rootfs || abort "! Alpine rootfs 解压失败"

# 离线 apk（linux-pam / shadow）塞进容器 /tmp
mv $MODPATH/apk $rootfs/tmp 2>/dev/null
rm -f $MODPATH/rootfs.tar.gz 2>/dev/null

ui_print "- 正在联网安装面板运行依赖..."

# DNS / hosts 准备
cp /system/etc/hosts $rootfs/etc/ 2>/dev/null
echo "nameserver 223.5.5.5" > $rootfs/etc/resolv.conf

RURIMA="$MODPATH/system/bin/rurima"
chmod +x "$RURIMA" 2>/dev/null

"$RURIMA" ruri -p -N -S -A $rootfs /bin/ash << 'EOF'
export HOME=/root
export LANG=C.UTF-8
export DAIDAI_DIR=/app/Dumb-Panel

# 切到 NJU Alpine 镜像源
sed -i 's|dl-cdn.alpinelinux.org|mirrors.nju.edu.cn|g' /etc/apk/repositories

# 先装离线包（linux-pam / shadow），再联网装剩下的
apk add --allow-untrusted --no-network /tmp/apk/*.apk 2>/dev/null && rm -rf /tmp/apk

apk add --no-cache \
  bash bash-completion coreutils build-base \
  curl wget git jq openssh openssl libtool \
  python3 python3-dev py3-pip \
  nodejs npm \
  shadow tzdata procps netcat-openbsd

# Android AID 组兼容
for id in 3001 3002 3003 3004 3005; do
  groupadd -g $id aid_$id 2>/dev/null || true
done
usermod -a -G aid_3001,aid_3002,aid_3003,aid_3004,aid_3005 root 2>/dev/null || true

# SSH 凭据（ports.conf 可自定义，这里用默认值）
SSH_USER="${SSH_USER:-root}"
SSH_PASSWORD="${SSH_PASSWORD:-123456}"
echo "${SSH_USER}:${SSH_PASSWORD}" | chpasswd 2>/dev/null
echo '123456' | chsh root -s /bin/bash 2>/dev/null
cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime 2>/dev/null

# SSH 基础配置
sed -i -e 's/^#PermitRootLogin.*/PermitRootLogin yes/' \
       -e 's/^#PasswordAuthentication/PasswordAuthentication/' \
       /etc/ssh/sshd_config 2>/dev/null
ssh-keygen -A 2>/dev/null

# 常用镜像源
npm config set registry https://registry.npmmirror.com 2>/dev/null
git config --global user.email "daidai@users.noreply.github.com"
git config --global user.name "daidai"
git config --global http.postBuffer 524288000

mkdir -p /app /app/web /app/Dumb-Panel
EOF

# 容器里补一份默认 bashrc
cat > $rootfs/etc/bash/bashrc << 'EOF'
export HOME=/root
export LANG=C.UTF-8
export SHELL=/bin/bash
export PS1='\u@\h:\w\$ '
export DAIDAI_DIR=/app/Dumb-Panel
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export NODE_PATH=/usr/local/lib/node_modules
EOF

# ---- 回填用户数据 -------------------------------------------------------
if [ -d "$TMPDIR/backup_data" ]; then
  ui_print "- 正在恢复用户数据..."
  mkdir -p $rootfs/app/Dumb-Panel
  cp -rf $TMPDIR/backup_data/* $rootfs/app/Dumb-Panel/ 2>/dev/null
  rm -rf $TMPDIR/backup_data
fi

# module.prop 同步一份给容器内 (supply to updater)
mkdir -p $rootfs/app
cp -f $MODPATH/module.prop $rootfs/app/module.prop 2>/dev/null

# ---- 持久化数据目录 ------------------------------------------------------
mkdir -p "$PERSIST_DIR"

# 把新版本的 module.prop 也落一份到持久化目录，作为 get_current_version() 的兜底，
# 下次升级就算管理器路径差异也能读到正确的旧版本号。
cp -f "$MODPATH/module.prop" "$PERSIST_DIR/module.prop" 2>/dev/null || true

# ---- 默认端口配置（用户可编辑 ports.conf 自定义端口，重启模块后生效） ----
if [ ! -f "$PERSIST_DIR/ports.conf" ]; then
  cat > "$PERSIST_DIR/ports.conf" << 'PCONF'
# 呆呆面板端口配置 —— 修改后重启模块生效
#
# PANEL_PORT: 面板 HTTP 端口（浏览器访问端口），默认 5700
#             后端绑定的是 0.0.0.0:PANEL_PORT，局域网 / 穿透都能直连
# SSH_PORT:   容器内 SSH 端口（adb/termux 登入容器调试），默认 22
# SSH_USER:   SSH 登录用户名，默认 root
# SSH_PASSWORD: SSH 登录密码，默认 123456（建议修改！）
# EXTRA_CORS_ORIGINS:
#             额外的 CORS 白名单；默认 127.0.0.1 / localhost 已放行，
#             且"同源请求"会被中间件自动放行，绝大多数内网穿透不需要改它。
#             以下两种情况再补：
#               1) 穿透侧端口与面板端口不同（例如 frp 公网 6700 → 内网 5700）
#               2) 用跨域模式访问（浏览器 Origin 与后端 Host 不一致）
#             用英文逗号分隔，建议加引号，示例：
#               EXTRA_CORS_ORIGINS="https://panel.example.com,https://xx.trycloudflare.com"
PANEL_PORT=5700
SSH_PORT=22
SSH_USER=root
SSH_PASSWORD=123456
EXTRA_CORS_ORIGINS=""
PCONF
fi

# 读一下当前配置，用于提示
CUR_PANEL_PORT=5700
CUR_SSH_PORT=22
# shellcheck disable=SC1090
. "$PERSIST_DIR/ports.conf" 2>/dev/null || true
CUR_PANEL_PORT="${PANEL_PORT:-5700}"
CUR_SSH_PORT="${SSH_PORT:-22}"

# ---- 收尾 --------------------------------------------------------------
"$RURIMA" ruri -w -U $rootfs 2>/dev/null || true

ui_print ""
ui_print "- 安装完成！"
ui_print "- 重启后面板将自动启动，访问 http://127.0.0.1:${CUR_PANEL_PORT}"
ui_print "- 端口配置: $PERSIST_DIR/ports.conf (PANEL_PORT=${CUR_PANEL_PORT}, SSH_PORT=${CUR_SSH_PORT})"
ui_print "- SSH 连接: ssh ${SSH_USER:-root}@<设备IP> -p ${CUR_SSH_PORT} (默认密码: ${SSH_PASSWORD:-123456})"
ui_print "- rootfs 位置: $rootfs"
ui_print "- 数据目录:   $rootfs/app/Dumb-Panel"
ui_print ""
