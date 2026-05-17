FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

ARG TARGETPLATFORM
ARG USER_ID=1000
ARG GROUP_ID=1000

RUN addgroup -g ${GROUP_ID} app && \
    adduser -D -u ${USER_ID} -G app app

COPY $TARGETPLATFORM/varc /varc

USER app

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/varc", "--help"]

STOPSIGNAL SIGTERM

CMD ["/varc"]
