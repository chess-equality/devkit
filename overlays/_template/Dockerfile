FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive

# Install essentials for Git over SSH via HTTP proxy, plus Python 3 and curl
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      bash git openssh-client ca-certificates netcat-openbsd \
      python3 python3-venv python3-pip curl \
 && rm -rf /var/lib/apt/lists/*

# Install uv (provides `uv` and `uvx`) to /usr/local/bin
RUN curl -fsSL https://astral.sh/uv/install.sh -o /tmp/install-uv.sh \
 && env UV_INSTALL_DIR=/usr/local/bin sh /tmp/install-uv.sh \
 && rm -f /tmp/install-uv.sh \
 && /usr/local/bin/uv --version && /usr/local/bin/uvx --version

# Create an unprivileged user (uid:gid 1000) and workspace dir
RUN useradd -m -u 1000 -s /bin/bash dev \
 && mkdir -p /workspace \
 && chown -R dev:dev /workspace

USER 1000:1000
WORKDIR /workspace

# Keep container alive for tmux/exec windows
CMD ["bash", "-lc", "sleep infinity"]
