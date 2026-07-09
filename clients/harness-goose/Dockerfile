# Real harness image: goose v1.39.0 serving ACP over HTTP/WebSocket.
# v1.39.0 = plain HTTP (the self-signed-TLS default landed later), auth via
# X-Secret-Key header or ?token= query param, per ADR 0002.
FROM debian:bookworm-slim

ARG GOOSE_VERSION=v1.39.0
ARG GOOSE_ARCH=aarch64-unknown-linux-gnu

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl bzip2 git libxcb1 \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL \
      "https://github.com/aaif-goose/goose/releases/download/${GOOSE_VERSION}/goose-${GOOSE_ARCH}.tar.bz2" \
      -o /tmp/goose.tar.bz2 \
    && tar -xjf /tmp/goose.tar.bz2 -C /usr/local/bin \
    && rm /tmp/goose.tar.bz2 \
    && chmod +x /usr/local/bin/goose \
    && goose --version

# Headless defaults; the controller overrides per run.
ENV GOOSE_DISABLE_KEYRING=1 \
    GOOSE_MODE=auto \
    HOME=/root

# Agent-base entrypoint: clones the run's repository into /workspace and
# maps the controller's KONVEYOR_* env contract onto goose before serving.
COPY entrypoint.sh /usr/local/bin/agent-entrypoint.sh
RUN chmod +x /usr/local/bin/agent-entrypoint.sh

WORKDIR /workspace
EXPOSE 4000
CMD ["/usr/local/bin/agent-entrypoint.sh"]
