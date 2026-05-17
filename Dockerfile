FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

ARG TARGETPLATFORM
ARG USER_ID=1000
ARG GROUP_ID=1000

RUN addgroup -g ${GROUP_ID} app && \
    adduser -D -u ${USER_ID} -G app app

COPY $TARGETPLATFORM/rclone-vfs /rclone-vfs

USER app

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/rclone-vfs", "--help"]

STOPSIGNAL SIGTERM

CMD ["/rclone-vfs"]
