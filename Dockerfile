FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

ARG TARGETPLATFORM

COPY $TARGETPLATFORM/rclone-vfs /rclone-vfs

CMD ["/rclone-vfs"]
