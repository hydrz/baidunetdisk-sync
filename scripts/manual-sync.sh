#!/bin/bash
# 手动触发一次即时双向同步
echo "正在手动触发 Rclone 双向同步..."
docker exec -it baidunetdisk-rclone-sync rclone bisync /data/local openlist_dav: --create-empty-src-dirs -v
