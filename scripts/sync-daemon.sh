#!/bin/sh
set -e

export HOME=/config/rclone
export RCLONE_CACHE_DIR=/config/rclone/.cache

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 启动 Rclone 百度网盘同步空间守护进程..."

mkdir -p /config/rclone /config/rclone/.cache /data/local

# 等待 OpenList WebDAV 服务就绪
echo "[$(date '+%Y-%m-%d %H:%M:%S')] 正在检测 OpenList WebDAV (${WEBDAV_URL}) 连通性..."
while ! nc -z openlist 5244 >/dev/null 2>&1; do
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 等待 OpenList 容器启动中 (端口 5244)..."
  sleep 3
done

# 生成 rclone 配置
OBSCURED_PASS=$(rclone obscure "${WEBDAV_PASSWORD}")

cat <<EOF > /config/rclone/rclone.conf
[openlist_dav]
type = webdav
url = ${WEBDAV_URL}/${WEBDAV_WORKSPACE_PATH}
vendor = other
user = ${WEBDAV_USER}
pass = ${OBSCURED_PASS}
EOF

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Rclone 配置就绪: openlist_dav:${WEBDAV_WORKSPACE_PATH}"

# 等待 WebDAV 路径可访问（OpenList 初始化存储可能需要几秒）
echo "[$(date '+%Y-%m-%d %H:%M:%S')] 正在测试 WebDAV 挂载点..."
RETRY=0
MAX_RETRY=30
while [ $RETRY -lt $MAX_RETRY ]; do
  if rclone lsd openlist_dav: >/dev/null 2>&1; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] WebDAV 挂载点连接成功！"
    break
  fi
  RETRY=$((RETRY + 1))
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] WebDAV 尚未挂载好，重试中 ($RETRY/$MAX_RETRY)..."
  sleep 4
done

if [ $RETRY -ge $MAX_RETRY ]; then
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 警告: 尚未在 OpenList 中检测到 /${WEBDAV_WORKSPACE_PATH} 存储，请确保已在 OpenList 管理后台配置好该存储。"
fi

# 检查是否已有初始基准同步文件或强制 RESYNC
INIT_MARKER="/config/rclone/.bisync_initialized"

if [ ! -f "$INIT_MARKER" ] || [ "$RESYNC_ON_START" = "true" ]; then
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 执行首次基准对齐 (rclone bisync --resync)..."
  if rclone bisync /data/local openlist_dav: --create-empty-src-dirs --resync -v; then
    touch "$INIT_MARKER"
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 首次基准对齐成功！"
  else
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 首次对齐未完全成功，将在主循环中继续尝试。"
  fi
fi

# 持续自动双向同步循环
echo "[$(date '+%Y-%m-%d %H:%M:%S')] 进入自动双向增量同步循环 (周期: ${SYNC_INTERVAL} 秒)..."
while true; do
  sleep "${SYNC_INTERVAL}"
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 触发双向增量同步..."
  if [ ! -f "$INIT_MARKER" ]; then
    if rclone bisync /data/local openlist_dav: --create-empty-src-dirs --resync -v; then
      touch "$INIT_MARKER"
    fi
  else
    rclone bisync /data/local openlist_dav: --create-empty-src-dirs -v || {
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] 提示: 同步异常或发现冲突文件，已保持本地安全。"
    }
  fi
done
