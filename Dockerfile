FROM --platform=$BUILDPLATFORM node:24.21.0-bookworm-slim AS frontend-builder

WORKDIR /build
COPY web/package.json web/package-lock.json ./
RUN npm ci --registry https://registry.npmmirror.com
COPY web/ ./
RUN npm run build


FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS backend-builder

RUN apk add --no-cache git

WORKDIR /build
COPY server/go.mod server/go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY server/ ./
ARG VERSION=2.2.18
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
RUN GOARM=$(case "${TARGETVARIANT}" in v7) echo 7;; v6) echo 6;; v5) echo 5;; *) echo '';; esac) && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${GOARM} \
    go build -ldflags="-s -w -X daidai-panel/handler.Version=${VERSION}" -o daidai-server . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${GOARM} \
    go build -ldflags="-s -w -X daidai-panel/handler.Version=${VERSION}" -o ddp ./cmd/ddp


FROM alpine:3.23

ARG TARGETARCH
ARG TARGETVARIANT
ARG PYTHON_STANDALONE_RELEASE=20260602
ARG PYTHON_RUNTIME_310=3.10.20
ARG PYTHON_RUNTIME_311=3.11.15
ARG PYTHON_RUNTIME_312=3.12.13
ARG PYTHON_RUNTIME_MODE=single
ARG PYTHON_RUNTIME_VERSION=3.12
ARG INSTALL_FULL_TOOLS=false

# 精简镜像只保留面板运行、脚本执行、仓库订阅和容器降权所需工具。
RUN apk add --no-cache \
    ca-certificates tzdata bash curl \
    gcompat libstdc++ \
    nginx \
    nodejs npm \
    git openssh-client-default \
    su-exec shadow

# 完整版才安装 Go、Docker CLI、下载工具与原生扩展编译链。
# Alpine 基础镜像自带 BusyBox wget，精简档必须显式移除这个入口。
RUN case "${INSTALL_FULL_TOOLS}" in \
      true) \
        apk add --no-cache \
          go docker-cli wget \
          build-base linux-headers pkgconf; \
        command -v go >/dev/null; \
        command -v gofmt >/dev/null; \
        command -v docker >/dev/null; \
        command -v gcc >/dev/null; \
        command -v g++ >/dev/null; \
        command -v make >/dev/null; \
        command -v pkg-config >/dev/null; \
        wget --version 2>&1 | grep -q "GNU Wget"; \
        ;; \
      false) \
        rm -f /usr/bin/wget; \
        for command_name in go gofmt docker wget gcc g++ make pkg-config; do \
          if command -v "$command_name" >/dev/null 2>&1; then \
            echo "精简镜像不应包含 $command_name" >&2; \
            exit 1; \
          fi; \
        done; \
        ;; \
      *) \
        echo "INSTALL_FULL_TOOLS 只允许为 true 或 false，当前值：${INSTALL_FULL_TOOLS}" >&2; \
        exit 1; \
        ;; \
    esac

# python-build-standalone 没有 Alpine 32 位资产；只有默认 3.12 镜像可使用发行版 Python。
RUN use_distro_python=false; \
    if [ "${PYTHON_RUNTIME_MODE}" = "single" ] && [ "${PYTHON_RUNTIME_VERSION}" = "3.12" ]; then \
      case "${TARGETARCH}/${TARGETVARIANT}" in \
        386/|arm/v7) use_distro_python=true ;; \
      esac; \
    fi; \
    if [ "$use_distro_python" = "true" ]; then \
      apk add --no-cache python3 py3-pip; \
      apk info --installed python3 >/dev/null; \
    elif apk info --installed python3 >/dev/null 2>&1 || command -v python3 >/dev/null 2>&1; then \
      echo "当前镜像必须只保留独立 Python，但系统 python3 包已被其他依赖引入" >&2; \
      exit 1; \
    fi

COPY docker/install-python-runtimes.sh /tmp/install-python-runtimes.sh
RUN sh /tmp/install-python-runtimes.sh alpine "${TARGETARCH}" "${TARGETVARIANT}" "${PYTHON_STANDALONE_RELEASE}" "${PYTHON_RUNTIME_310}" "${PYTHON_RUNTIME_311}" "${PYTHON_RUNTIME_312}" "${PYTHON_RUNTIME_MODE}" "${PYTHON_RUNTIME_VERSION}" \
    && rm -f /tmp/install-python-runtimes.sh

RUN mkdir -p /app/Dumb-Panel/scripts /app/Dumb-Panel/logs /app/Dumb-Panel/backups /run/nginx /tmp && chmod 1777 /tmp

WORKDIR /app

COPY --from=backend-builder /build/daidai-server .
COPY --from=backend-builder /build/ddp /usr/local/bin/ddp
COPY --from=backend-builder /build/config.yaml .
COPY --from=frontend-builder /build/dist /app/web
COPY docker/nginx.conf /etc/nginx/http.d/default.conf
COPY docker/entrypoint.sh /app/entrypoint.sh
COPY docker/healthcheck.sh /app/healthcheck.sh

RUN chmod +x /app/entrypoint.sh /app/healthcheck.sh /usr/local/bin/ddp && sed -i 's/\r$//' /app/entrypoint.sh /app/healthcheck.sh

ENV TZ=Asia/Shanghai
# 统一字符编码，避免 docker exec 终端中文文件名/输出乱码（Alpine musl ≥1.2.3 内置 C.UTF-8）。
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8
ENV PANEL_PORT=5700
ENV DAIDAI_PYTHON_RUNTIME_MODE=${PYTHON_RUNTIME_MODE}
ENV DAIDAI_PYTHON_VERSION=${PYTHON_RUNTIME_VERSION}
ENV DAIDAI_PYTHON_RUNTIME_ROOT=/opt/daidai-python

EXPOSE ${PANEL_PORT}

VOLUME ["/app/Dumb-Panel"]

# 容器健康检查：飞牛 OS / 群晖等 NAS 容器面板依赖此标记容器状态。探测地址跟着 PANEL_HOST 走（#162）。
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD /app/healthcheck.sh || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
