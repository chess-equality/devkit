FROM alpine:3.19
RUN apk add --no-cache tinyproxy bash netcat-openbsd curl python3 && \
    mkdir -p /var/run/tinyproxy /var/log/tinyproxy
    
# Install uv (provides `uv` and `uvx`) to /usr/local/bin
RUN curl -fsSL https://astral.sh/uv/install.sh -o /tmp/install-uv.sh \
 && UV_INSTALL_DIR=/usr/local/bin sh /tmp/install-uv.sh \
 && rm -f /tmp/install-uv.sh \
 && /usr/local/bin/uv --version && /usr/local/bin/uvx --version || true
COPY kit/proxy/tinyproxy.conf /etc/tinyproxy/tinyproxy.conf
COPY kit/proxy/allowlist.txt /etc/tinyproxy/allowlist.txt
EXPOSE 8888
CMD ["tinyproxy", "-d", "-c", "/etc/tinyproxy/tinyproxy.conf"]
