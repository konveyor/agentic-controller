# Real harness image: goose v1.39.0 serving ACP over HTTP/WebSocket.
# v1.39.0 = plain HTTP (the self-signed-TLS default landed later), auth via
# X-Secret-Key header or ?token= query param, per ADR 0002.
FROM debian:bookworm-slim

ARG GOOSE_VERSION=v1.39.0
# Release-triple override; when empty the RUN below derives it from
# TARGETARCH (BuildKit) or dpkg (classic builder).
ARG GOOSE_ARCH=
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl bzip2 git libxcb1 \
    && rm -rf /var/lib/apt/lists/*

RUN arch="${GOOSE_ARCH}"; \
    if [ -z "$arch" ]; then \
      case "${TARGETARCH:-$(dpkg --print-architecture)}" in \
        amd64) arch=x86_64-unknown-linux-gnu ;; \
        arm64) arch=aarch64-unknown-linux-gnu ;; \
        *) echo "unsupported arch '${TARGETARCH}' — set GOOSE_ARCH explicitly" >&2; exit 1 ;; \
      esac; \
    fi; \
    curl -fsSL \
      "https://github.com/aaif-goose/goose/releases/download/${GOOSE_VERSION}/goose-${arch}.tar.bz2" \
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
