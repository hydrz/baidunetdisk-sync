FROM alpine:latest

WORKDIR /app

COPY baidunetdisk-sync /app/baidunetdisk-sync

RUN chmod +x /app/baidunetdisk-sync && mkdir -p /data

ENV LOCAL_SYNC_DIR=/data \
    REMOTE_ROOT=/_pcs_.workspace \
    SYNC_INTERVAL=300 \
    CONCURRENCY=4 \
    TZ=Asia/Shanghai

ENTRYPOINT ["/app/baidunetdisk-sync"]
CMD ["-daemon"]
