# Migration Harness Container
# Multi-stage build: compile Go binary, then copy into runtime image
# Supports Java, Python, .NET, Node.js migrations

# --- Build stage ---
FROM golang:1.23 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -o /migration-harness ./cmd/migration-harness/

# --- Runtime stage ---
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    wget \
    git \
    unzip \
    ca-certificates \
    gnupg \
    software-properties-common \
    build-essential \
    gcc \
    g++ \
    make \
    python3 \
    python3-pip \
    python3-venv \
    python3-dev \
    libffi-dev \
    pkg-config \
    && rm -rf /var/lib/apt/lists/*

# Install graphifyy (code graph generator - required for detect step)
RUN python3 -m pip install --break-system-packages --no-cache-dir graphifyy==0.7.17

# Install Java (OpenJDK 21)
RUN apt-get update && apt-get install -y \
    openjdk-21-jdk \
    maven \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 20 LTS
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install .NET 8 SDK
RUN wget https://packages.microsoft.com/config/ubuntu/24.04/packages-microsoft-prod.deb -O packages-microsoft-prod.deb \
    && dpkg -i packages-microsoft-prod.deb \
    && rm packages-microsoft-prod.deb \
    && apt-get update \
    && apt-get install -y dotnet-sdk-8.0 \
    && rm -rf /var/lib/apt/lists/*

# Install Goose (Block's LLM orchestrator)
RUN apt-get update && apt-get install -y bzip2 \
    && mkdir -p /usr/local/bin \
    && ARCH=$(uname -m) \
    && if [ "$ARCH" = "x86_64" ]; then GOOSE_ARCH="x86_64-unknown-linux-gnu"; \
       elif [ "$ARCH" = "aarch64" ]; then GOOSE_ARCH="aarch64-unknown-linux-gnu"; \
       else echo "Unsupported architecture: $ARCH" && exit 1; fi \
    && curl -fsSL "https://github.com/block/goose/releases/download/stable/goose-${GOOSE_ARCH}.tar.bz2" -o /tmp/goose.tar.bz2 \
    && tar -xjf /tmp/goose.tar.bz2 -C /tmp \
    && mv /tmp/goose /usr/local/bin/goose \
    && chmod +x /usr/local/bin/goose \
    && rm -f /tmp/goose.tar.bz2 \
    && rm -rf /var/lib/apt/lists/*

RUN JAVA_HOME_PATH=$(dirname $(dirname $(readlink -f $(which java)))) \
    && ln -sf "$JAVA_HOME_PATH" /usr/lib/jvm/java-current
ENV JAVA_HOME=/usr/lib/jvm/java-current
ENV PATH="${JAVA_HOME}/bin:${PATH}"
ENV PYTHONUNBUFFERED=1

# Copy Go binary from build stage
COPY --from=builder /migration-harness /usr/local/bin/migration-harness

# Copy recipes and skill bundle (resolved relative to binary location)
COPY recipes/ /usr/local/bin/recipes/
COPY skill-bundle/ /usr/local/bin/skill-bundle/

RUN groupadd -r harness && useradd -r -g harness -d /home/harness -s /sbin/nologin harness \
    && mkdir -p /home/harness/.migration-harness /workspace \
    && chown -R harness:harness /home/harness /workspace

WORKDIR /workspace

USER harness

ENTRYPOINT ["migration-harness"]
CMD ["--help"]
