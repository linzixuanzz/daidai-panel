#!/usr/bin/env bash
##########################################################################
# 呆呆面板 Magisk 模块打包脚本 (容器方案 v2.0.6+)
#
# 用法（版本号必填）:
#   bash Magisk/build.sh 3.2.7                # arm64 + alpine
#   bash Magisk/build.sh 3.2.7 all            # 同时打包 arm64 + amd64
#   bash Magisk/build.sh 3.2.7 arm64 debian   # Debian(glibc) flavor
#
# 自检（不构建、不打包、不需要 go）:
#   bash Magisk/build.sh --check-dist         # 只校验已存在的 web/dist 是不是发布版产物
#
# 产物:
#   alpine（默认）: dist/daidai-panel-magisk-v<版本>.zip
#   debian        : dist/daidai-panel-magisk-debian-v<版本>.zip
#
# 模块内部不再内置 Python/Node；改为在 customize.sh 里用 rurima + rootfs 构建一个
# 容器，再用 apk / apt-get 装出 python3 / nodejs / npm / git 等：
#   alpine —— Alpine 3.18 minirootfs（musl，体积小）
#   debian —— CI 自建的 Debian bookworm 精简 rootfs（glibc，能跑官方预编译产物）
##########################################################################

set -euo pipefail

# --check-dist：只跑「已存在的 web/dist 是不是发布版产物」这一道前置检查然后退出。
# 不构建、不打包，也【不要求 go / npm / zip 在 PATH 上】—— 后面那些 command -v 检查
# 排在守卫前面，不给这条早退路径的话，守卫在没配 Go 的机器上根本没法被单独跑一次。
#
# 它存在的唯一理由是让这道守卫可以被验证【失败一次】：
#   (cd web && npm run build:demo) && bash Magisk/build.sh --check-dist   # 期望 exit 1
#   (cd web && npm run build)      && bash Magisk/build.sh --check-dist   # 期望 exit 0
# 参见 .trellis/spec/guides/cross-layer-thinking-guide.md 的
# 「一条永远为真的断言等于没有断言」。
CHECK_DIST_ONLY=0
if [ "${1:-}" = "--check-dist" ]; then
  CHECK_DIST_ONLY=1
  shift
fi

# 版本号必填。原来这里有个默认值，但它每次发版都会漏更新（v3.0.1 就漏了），
# 结果本地不传参会打出一个标着旧版本号的包 —— 那种包看不出错，装上才发现不对。
# CI 一直是显式传参的（release.yml 的 magisk-module job），所以改成必填不影响它。
if [ "$CHECK_DIST_ONLY" = "1" ]; then
  # --check-dist 不产出任何文件，版本号在这条路径上没有意义，不要为它设门槛。
  VERSION="${1:-0.0.0}"
else
  VERSION="${1:?用法: bash Magisk/build.sh <版本号> [arm64|amd64|all] [alpine|debian]}"
fi
TARGETS="${2:-arm64}"     # arm64 / amd64 / all
FLAVOR="${3:-alpine}"     # alpine / debian —— 不传时行为与产物名与历史完全一致

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

# alpine 走空后缀，保证默认产物名 / staging 目录名与历史版本逐字节一致
case "$FLAVOR" in
  alpine) FLAVOR_SUFFIX="" ;;
  debian) FLAVOR_SUFFIX="-debian" ;;
  *) printf "\033[1;31m[ERR ]\033[0m 未知 flavor: %s （支持: alpine / debian）\n" "$FLAVOR" >&2; exit 1 ;;
esac

MODDIR="$ROOT/Magisk"
DIST="$ROOT/dist"
STAGING="$DIST/magisk-staging${FLAVOR_SUFFIX}"
OUTZIP="$DIST/daidai-panel-magisk${FLAVOR_SUFFIX}-v${VERSION}.zip"

info()  { printf "\033[1;32m[INFO]\033[0m %s\n" "$*" >&2; }
warn()  { printf "\033[1;33m[WARN]\033[0m %s\n" "$*" >&2; }
error() { printf "\033[1;31m[ERR ]\033[0m %s\n" "$*" >&2; }

# ⚠️ 复用已存在的 web/dist 之前必须先确认它是【发布版】产物。
#    web 侧有两条构建，写的是【同一个】 web/dist：
#      npm run build       -> 发布版：无 mock 层、robots=noindex、根相对路径
#      npm run build:demo  -> 在线演示 Demo：浏览器内 mock 层 + --base=/daidai-panel/
#    跑过 build:demo 之后直接跑本脚本，下面那句 cp web/dist 会把整套 mock 顶替层打进
#    模块 ZIP。装上去的表现是「面板能开、数据全是假的、一个错都不报」——比白屏难查得多。
#
#    .github/workflows/checks.yml 里那两条产物门禁够不着这条路径：CI 每次都是全新
#    runner，永远不存在「预先躺在那儿的 dist」，所以这个坑【只在本地复现】。
assert_release_dist() {
  local dist="$ROOT/web/dist"

  if [ ! -f "$dist/index.html" ] || [ ! -d "$dist/assets" ]; then
    error "web/dist 存在但结构不完整（缺 index.html 或 assets/），像是一次被中断的构建"
    error "解决：rm -rf web/dist 后重跑本脚本（会自动重新构建）"
    exit 1
  fi

  # 判据 1：Demo mock 层的哨兵字符串。
  # 必须用【字符串字面量】而不是函数名 / 模块路径 —— 后者会被 esbuild 压成单字母，
  # 或者压根不出现在产物里，拿它们做判据是恒不命中的空转门禁。
  # 缘由见 web/src/demo/marker.ts 顶部注释。
  local demo_hits
  demo_hits="$(grep -rl '__DAIDAI_DEMO_MOCK__' "$dist/assets" 2>/dev/null || true)"
  if [ -n "$demo_hits" ]; then
    error "web/dist 是 Demo 产物：命中 mock 哨兵 __DAIDAI_DEMO_MOCK__，绝不能打进模块 ZIP"
    printf '%s\n' "$demo_hits" | head -n 5 >&2 || true
    error "解决：rm -rf web/dist && (cd web && npm run build)，然后重跑本脚本"
    exit 1
  fi

  # 判据 2：robots meta。
  # 发布版恒为 noindex（web/index.html 里的静态默认值），只有 build:demo 会被
  # vite.config.ts 的 robotsMetaPlugin 改写成 index, follow。
  # 它和判据 1 有重叠，留着是为了守「哨兵哪天被改名 / 被 tree-shake 掉」的情况。
  if grep -qE '<meta[^>]+name="robots"[^>]+content="index, follow"' "$dist/index.html"; then
    error "web/dist 的 robots meta 是 index, follow —— 这是 Demo 构建专有的改写，发布版恒为 noindex"
    error "解决：rm -rf web/dist && (cd web && npm run build)，然后重跑本脚本"
    exit 1
  fi

  # 判据 3：子路径构建。
  # 前两条只认得出 Demo，认不出「没有 mock 层、但带了 base 前缀」的产物
  # （例如有人手动跑 npx vite build --base=/daidai-panel/ 做验证后忘了还原）。
  # 模块把前端挂在服务根上，带前缀的产物装上去是【所有资源 404】。
  if ! grep -qE '<script[^>]+src="/assets/' "$dist/index.html"; then
    error "web/dist 的入口脚本不是根相对路径（/assets/...），疑似子路径构建（--base=...）"
    error "模块把前端挂在服务根上，带 base 前缀的产物装上去所有资源都会 404"
    error "index.html 里实际的入口脚本如下："
    grep -oE '<script[^>]*src="[^"]*"' "$dist/index.html" | head -n 3 >&2 || true
    error "解决：rm -rf web/dist && (cd web && npm run build)，然后重跑本脚本"
    exit 1
  fi

  info "web/dist 校验通过：发布版产物（无 mock 哨兵 / robots=noindex / 根相对路径）"
}

if [ "$CHECK_DIST_ONLY" = "1" ]; then
  if [ ! -d "$ROOT/web/dist" ]; then
    info "web/dist 不存在 —— 没有可复用的产物，本检查无对象（真正打包时会自动构建发布版）"
    exit 0
  fi
  assert_release_dist
  exit 0
fi

command -v go   >/dev/null || { error "缺少 go"; exit 1; }
command -v npm  >/dev/null || { error "缺少 npm"; exit 1; }

# Windows Git Bash 下通常没有 zip，用 python 兜底打包
PY_FALLBACK=""
if command -v py >/dev/null; then
  PY_FALLBACK="py"
elif command -v python3 >/dev/null; then
  PY_FALLBACK="python3"
elif command -v python >/dev/null; then
  if python -c "print(1)" >/dev/null 2>&1; then
    PY_FALLBACK="python"
  fi
fi
if ! command -v zip >/dev/null; then
  if [ -z "$PY_FALLBACK" ]; then
    error "缺少 zip 且未找到可用 python，请安装其一"
    exit 1
  fi
  warn "未找到 zip，将使用 $PY_FALLBACK 做 ZIP 打包"
fi

# 1. 前端构建
# assert_release_dist 的定义与「为什么需要它」在文件上方（挪到 command -v go 之前，
# 好让 --check-dist 在没配 Go 的机器上也能单独跑）。
if [ ! -d "$ROOT/web/dist" ]; then
  info "前端 dist 不存在，开始构建..."
  (cd "$ROOT/web" && npm ci && npm run build)
else
  info "已存在 web/dist，跳过前端构建（如需强制重建请先删除 web/dist）"
  # 刻意【不】替用户重建：一次误操作换来一次几分钟的静默重建，用户根本不知道
  # 自己的 dist 被换掉了。直接 exit 1 说清楚怎么办，比"贴心"地帮他修更安全。
  assert_release_dist
fi

# 2. 后端交叉编译（Alpine musl 环境下也能跑 CGO_ENABLED=0 的 Go 静态二进制）
rm -rf "$STAGING"
mkdir -p "$STAGING/system/bin" "$STAGING/web" "$DIST"

build_backend() {
  local go_arch="$1"
  local suffix="$2"
  info "编译后端: GOOS=linux GOARCH=${go_arch}"
  (cd "$ROOT/server" && \
    CGO_ENABLED=0 GOOS=linux GOARCH="${go_arch}" \
    go build -trimpath \
      -ldflags="-s -w -X daidai-panel/handler.Version=${VERSION}" \
      -o "$STAGING/system/bin/daidai-server-${suffix}" .)
  (cd "$ROOT/server" && \
    CGO_ENABLED=0 GOOS=linux GOARCH="${go_arch}" \
    go build -trimpath \
      -ldflags="-s -w -X daidai-panel/handler.Version=${VERSION}" \
      -o "$STAGING/system/bin/ddp-${suffix}" ./cmd/ddp)
}

case "$TARGETS" in
  arm64) build_backend arm64 arm64 ;;
  amd64) build_backend amd64 amd64 ;;
  all)
    build_backend arm64 arm64
    build_backend amd64 amd64
    ;;
  *) error "未知架构: $TARGETS （支持: arm64 / amd64 / all）"; exit 1 ;;
esac

# 3. 拷贝模块文件（Git Bash 上 *.sh 可能带 CRLF，BusyBox sh 解析不了，统一过 tr 一遍）
info "打包模块文件... (flavor=$FLAVOR)"
copy_sh() {
  tr -d '\r' < "$1" > "$2"
  chmod +x "$2" 2>/dev/null || true
}

copy_sh "$MODDIR/customize.sh"                       "$STAGING/customize.sh"
copy_sh "$MODDIR/service.sh"                         "$STAGING/service.sh"
copy_sh "$MODDIR/uninstall.sh"                       "$STAGING/uninstall.sh"
copy_sh "$MODDIR/action.sh"                          "$STAGING/action.sh"
cp -f   "$MODDIR/module.prop"                        "$STAGING/module.prop"
[ -f "$MODDIR/README.md" ] && cp -f "$MODDIR/README.md" "$STAGING/README.md"

# flavor 标记文件：customize.sh / service.sh / action.sh 都读它来决定容器 rootfs
# 来源、容器内 shell、包管理器。刻意不用 sed 把 flavor 烤进脚本 —— 那样 ZIP 里的
# 脚本和仓库里的源码就不是同一份，排障时看到的和实际跑的对不上。
printf '%s\n' "$FLAVOR" > "$STAGING/flavor"

# 容器二进制（rurima）—— 从 Magisk/system/bin/ 拷到 staging/system/bin/
if [ -f "$MODDIR/system/bin/rurima" ]; then
  cp -f "$MODDIR/system/bin/rurima" "$STAGING/system/bin/rurima"
  chmod +x "$STAGING/system/bin/rurima"
else
  error "缺少 $MODDIR/system/bin/rurima（容器运行时），请先放置静态 rurima 二进制"
  exit 1
fi

# 离线 apk（linux-pam / shadow）—— 只有 alpine flavor 需要。
# 这两个包是 aarch64 Alpine 专用，Debian 侧同等能力由 apt 的 passwd / libpam 提供，
# 塞进 Debian ZIP 只会白白撑大体积、并让 customize.sh 里多一条永远走不到的分支。
if [ "$FLAVOR" = "alpine" ] && [ -d "$MODDIR/apk" ]; then
  mkdir -p "$STAGING/apk"
  cp -f "$MODDIR/apk/"*.apk "$STAGING/apk/" 2>/dev/null || true
fi

# scripts/
if [ -d "$MODDIR/scripts" ]; then
  mkdir -p "$STAGING/scripts"
  for f in "$MODDIR"/scripts/*; do
    [ -f "$f" ] || continue
    name="$(basename "$f")"
    case "$name" in
      *.sh) copy_sh "$f" "$STAGING/scripts/$name" ;;
      *)    cp -f "$f" "$STAGING/scripts/$name" ;;
    esac
  done
fi

# META-INF/
if [ -d "$MODDIR/META-INF" ]; then
  mkdir -p "$STAGING/META-INF/com/google/android"
  for f in "$MODDIR"/META-INF/com/google/android/*; do
    [ -f "$f" ] || continue
    name="$(basename "$f")"
    copy_sh "$f" "$STAGING/META-INF/com/google/android/$name"
  done
fi

# 同步版本号到 module.prop
# versionCode: 2.0.6 -> 20006 (MAJ*10000 + MIN*100 + PATCH)，与 CI 保持一致
IFS='.' read -r _MAJ _MIN _PATCH <<<"$VERSION"
_MAJ=${_MAJ:-0}; _MIN=${_MIN:-0}; _PATCH=${_PATCH:-0}
VERSIONCODE=$(( _MAJ * 10000 + _MIN * 100 + _PATCH ))
sed -i.bak \
  -e "s|^version=.*|version=v${VERSION}|" \
  -e "s|^versionCode=.*|versionCode=${VERSIONCODE}|" \
  "$STAGING/module.prop"
rm -f "$STAGING/module.prop.bak"

# updateJson 按 flavor 分开。
# 两个 flavor 共用同一个 module id（daidai-panel），而 updateJson 只能填一个 zipUrl，
# 所以以前 Debian 用户在管理器里点「更新」会被静默刷成 Alpine 版 —— 容器基础系统
# 直接从 glibc 换成 musl，装好的依赖全部失效。这里让 Debian ZIP 指向自己的
# update-debian.json，两条更新线彻底分开。
# 只把文件名从 update.json 换成 update-debian.json，仓库地址原样保留 ——
# fork 的用户会把 module.prop 里的 updateJson 改成自己的仓库，写死上游地址会把它覆盖掉。
if [ "$FLAVOR" = "debian" ]; then
  sed -i.bak \
    -e "s|^updateJson=\(.*\)/update\.json$|updateJson=\1/update-debian.json|" \
    "$STAGING/module.prop"
  rm -f "$STAGING/module.prop.bak"
  if ! grep -q '^updateJson=.*/update-debian\.json$' "$STAGING/module.prop"; then
    error "Debian flavor 的 updateJson 改写失败，请检查 Magisk/module.prop 里 updateJson 的写法"
    exit 1
  fi
fi

# 前端静态资源
# 这里没有任何校验，是因为上面的 assert_release_dist 已经在【复用已存在 dist】那条
# 分支上把关了（Demo mock 层 / robots / base 前缀三条判据）。改动那段时记得这里依赖它。
cp -rf "$ROOT/web/dist/"* "$STAGING/web/"

# 4. 打包 ZIP
rm -f "$OUTZIP"
info "生成 ZIP: $OUTZIP"
if command -v zip >/dev/null; then
  (cd "$STAGING" && zip -r9 "$OUTZIP" . -x "*.DS_Store")
else
  $PY_FALLBACK - "$STAGING" "$OUTZIP" <<'PY'
import os, sys, zipfile
staging, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for root, dirs, files in os.walk(staging):
        for f in files:
            if f == '.DS_Store':
                continue
            full = os.path.join(root, f)
            rel = os.path.relpath(full, staging).replace('\\', '/')
            z.write(full, rel)
print(f"wrote {out}")
PY
fi

info "完成: $OUTZIP (flavor=$FLAVOR)"
info "用法: 在 Magisk / KernelSU / APatch 管理器中选择此 ZIP 安装即可。"
