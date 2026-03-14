FROM node:20-alpine AS frontend-builder

WORKDIR /build
COPY web/package.json web/package-lock.json ./
RUN npm ci --registry https://registry.npmmirror.com
COPY web/ ./
RUN npm run build


FROM golang:1.25-alpine AS backend-builder

RUN apk add --no-cache git

WORKDIR /build
COPY server/go.mod server/go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY server/ ./
ARG VERSION=1.1.0
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X daidai-panel/handler.Version=${VERSION}" -o daidai-server .


FROM alpine:3.19

RUN apk add --no-cache \
    ca-certificates tzdata bash curl \
    nginx \
    python3 py3-pip \
    nodejs npm \
    git openssh-client \
    docker-cli

RUN mkdir -p /app/Dumb-Panel/scripts /app/Dumb-Panel/logs /app/Dumb-Panel/backups /run/nginx

WORKDIR /app

COPY --from=backend-builder /build/daidai-server .
COPY --from=backend-builder /build/config.yaml .
COPY --from=frontend-builder /build/dist /app/web
COPY docker/nginx.conf /etc/nginx/http.d/default.conf
COPY docker/entrypoint.sh /app/entrypoint.sh

RUN chmod +x /app/entrypoint.sh

ENV TZ=Asia/Shanghai
ENV PANEL_PORT=5700

EXPOSE 5700

VOLUME ["/app/Dumb-Panel"]

ENTRYPOINT ["/app/entrypoint.sh"]
