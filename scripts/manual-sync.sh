#!/bin/bash
set -e

# 参数处理
EXTRA_ARGS=""
if [ "$1" = "--dry-run" ]; then
  EXTRA_ARGS="--dry-run"
  echo "🔍 [预览模式] 仅比对变动，不实际修改本地和网盘文件..."
elif [ "$1" = "--resync" ]; then
  EXTRA_ARGS="--resync"
  echo "⚠️  [强制校准] 正在执行全量基准对齐 (--resync)..."
elif [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
  echo "用法: $0 [选项]"
  echo "选项:"
  echo "  (无参数)      执行一次常规双向增量同步"
  echo "  --dry-run    预览变动差异（试运行，不实际读写传输）"
  echo "  --resync     强制全量重对齐基准（解决不一致或首次建立快照）"
  exit 0
fi

FILTER_ARG=""
if [ -f "/scripts/filter.txt" ] || [ -f "$(dirname "$0")/filter.txt" ]; then
  FILTER_ARG="--filter-from /scripts/filter.txt"
fi

echo "正在触发 Rclone 双向同步..."
docker exec -e HOME=/config/rclone -it baidunetdisk-rclone-sync \
  rclone bisync /data/local openlist_dav: --create-empty-src-dirs $FILTER_ARG $EXTRA_ARGS -v
