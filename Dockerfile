FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

ARG TARGETPLATFORM

COPY $TARGETPLATFORM/vfscache_proxy /vfscache_proxy

CMD ["/vfscache_proxy"]
