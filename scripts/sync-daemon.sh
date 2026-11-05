#!/bin/sh
set -e

export HOME=/config/rclone
export RCLONE_CACHE_DIR=/config/rclone/.cache

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 启动 Rclone 百度网盘同步空间守护进程..."

mkdir -p /config/rclone /config/rclone/.cache /data/local

# 等待 OpenList WebDAV 端口可用
echo "[$(date '+%Y-%m-%d %H:%M:%S')] 正在等待 OpenList WebDAV (${WEBDAV_URL}) 就绪..."
while ! nc -z openlist 5244 >/dev/null 2>&1; do
  sleep 3
done

# 生成或更新 rclone.conf
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

FILTER_OPT=""
if [ -f "/scripts/filter.txt" ]; then
  FILTER_OPT="--filter-from /scripts/filter.txt"
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 已启用同步过滤规则: /scripts/filter.txt"
fi

# 等待 WebDAV 路径可访问（验证 OpenList 百度存储状态是否为 work）
echo "[$(date '+%Y-%m-%d %H:%M:%S')] 正在检测 WebDAV 百度网盘同步空间挂载点..."
RETRY=0
while true; do
  if rclone lsd openlist_dav: >/dev/null 2>&1; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 恭喜！WebDAV 百度同步空间连接成功！"
    break
  fi
  RETRY=$((RETRY + 1))
  if [ $((RETRY % 6)) -eq 0 ]; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 提示: 百度同步空间尚未就绪，请在 OpenList 控制台确认存储状态是否为 'work'。"
  fi
  sleep 5
done

# 首次基准同步标记文件
INIT_MARKER="/config/rclone/.bisync_initialized"

if [ ! -f "$INIT_MARKER" ] || [ "$RESYNC_ON_START" = "true" ]; then
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 正在执行首次基准校准 (rclone bisync --resync)..."
  if rclone bisync /data/local openlist_dav: --create-empty-src-dirs $FILTER_OPT --resync -v; then
    touch "$INIT_MARKER"
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 首次基准校准成功！"
  else
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 首次对齐未完全成功，将在主循环中重试。"
  fi
fi

# 持续自动双向同步循环
echo "[$(date '+%Y-%m-%d %H:%M:%S')] 进入自动双向增量同步循环 (周期: ${SYNC_INTERVAL} 秒)..."
while true; do
  sleep "${SYNC_INTERVAL}"
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] 开始触发双向增量同步..."
  if [ ! -f "$INIT_MARKER" ]; then
    if rclone bisync /data/local openlist_dav: --create-empty-src-dirs $FILTER_OPT --resync -v; then
      touch "$INIT_MARKER"
    fi
  else
    rclone bisync /data/local openlist_dav: --create-empty-src-dirs $FILTER_OPT -v || {
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] 提示: 同步遇到变动或临时网络抖动，保持本地数据安全，待下次循环继续。"
    }
  fi
done
