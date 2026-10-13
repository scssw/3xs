# syntax=docker/dockerfile:1.7
# ========================================================
# Stage: Builder
# ========================================================
FROM golang:1.26.3-alpine AS builder
WORKDIR /app
ARG TARGETARCH

# 1) 编译依赖（尽量稳定，放前面，能缓存）
RUN apk add --no-cache \
  build-base \
  gcc \
  curl \
  unzip

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"

# 2) 先只拷贝 go.mod/go.sum，依赖下载层就能稳缓存
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 3) 再拷贝其余源码（改代码只会影响后面的 build 层）
COPY . .

# 4) 编译（go build 自带 build cache mount，加速重复构建）
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags "-w -s" -o build/x-ui main.go


# ========================================================
# Stage: Final Image of 3x-ui
# ========================================================
FROM alpine:3.20
ENV TZ=Asia/Tehran
WORKDIR /app
ARG XRAY_VERSION=v26.4.25

# 5) 运行时依赖（这层只在你改这里或换 base 才会重新下载）
RUN apk add --no-cache \
  ca-certificates \
  tzdata \
  fail2ban \
  bash \
  curl \
  unzip
# --- install xray-core (linux-amd64) into /app/bin ---
RUN mkdir -p /app/bin \
  && XRAY_VER="${XRAY_VERSION}" \
  && curl -L -o /tmp/xray.zip "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VER}/Xray-linux-64.zip" \
  && unzip -o /tmp/xray.zip -d /tmp/xray \
  && mv /tmp/xray/xray /app/bin/xray-linux-amd64 \
  && mv /tmp/xray/geoip.dat /app/bin/geoip.dat \
  && mv /tmp/xray/geosite.dat /app/bin/geosite.dat \
  && chmod +x /app/bin/xray-linux-amd64 \
  && rm -rf /tmp/xray /tmp/xray.zip


# 6) 拷贝产物
COPY --from=builder /app/build/ /app/
COPY --from=builder /app/DockerEntrypoint.sh /app/DockerEntrypoint.sh
COPY --from=builder /app/x-ui.sh /usr/bin/x-ui

# 7) 修复 Windows CRLF + 权限（合并为一个 RUN，少一层）
RUN sed -i 's/\r$//' /app/DockerEntrypoint.sh \
  && chmod +x /app/DockerEntrypoint.sh /app/x-ui /usr/bin/x-ui

# 8) Configure fail2ban（保持原样，合并成一个 RUN）
RUN rm -f /etc/fail2ban/jail.d/alpine-ssh.conf \
  && cp /etc/fail2ban/jail.conf /etc/fail2ban/jail.local \
  && sed -i "s/^\[ssh\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/^\[sshd\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/#allowipv6 = auto/allowipv6 = auto/g" /etc/fail2ban/fail2ban.conf

RUN mkdir -p /app/bin && chmod 755 /app/bin
RUN mkdir -p /app/bin

ENV XUI_ENABLE_FAIL2BAN="true"
EXPOSE 2053/tcp
EXPOSE 2053/udp
VOLUME [ "/etc/x-ui" ]

ENTRYPOINT [ "/app/DockerEntrypoint.sh" ]
CMD [ "./x-ui" ]
