FROM alpine:3.19

RUN apk add --no-cache bash ca-certificates curl git openssh \
    && adduser -D -u 1000 dev

WORKDIR /workspace
COPY codex.sh /usr/local/bin/codex
RUN chmod +x /usr/local/bin/codex

USER dev
CMD ["sleep", "infinity"]
