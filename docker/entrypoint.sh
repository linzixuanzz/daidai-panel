#!/bin/sh

DATA_DIR=${DATA_DIR:-/app/Dumb-Panel}

mkdir -p "${DATA_DIR}/scripts" "${DATA_DIR}/logs" "${DATA_DIR}/backups"

PANEL_PORT=${PANEL_PORT:-5700}

sed -i "s/listen 5700/listen ${PANEL_PORT}/" /etc/nginx/http.d/default.conf

cat > /app/config.yaml <<YAML
server:
  port: 5701
  mode: release

database:
  path: ${DATA_DIR}/daidai.db

jwt:
  secret: ""
  access_token_expire: 480h
  refresh_token_expire: 1440h

data:
  dir: ${DATA_DIR}
  scripts_dir: ${DATA_DIR}/scripts
  log_dir: ${DATA_DIR}/logs

cors:
  origins:
    - http://localhost:5173
    - http://localhost:${PANEL_PORT}
YAML

nginx

exec /app/daidai-server
