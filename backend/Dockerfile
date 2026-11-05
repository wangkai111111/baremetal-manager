# 使用 Debian bookworm-slim（glibc）作为运行时镜像。
# Alpine 的 musl libc 在 ipmitool 1.8.19 + Go pipe exec 场景下会触发
# segfault，改为 glibc 基础镜像以确保 SOL 串口控制台稳定。
FROM golang:1.26-bookworm AS build
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

FROM debian:bookworm-slim
# 时区与 ipmitool 工具
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates ipmitool wget \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r bmm && useradd -r -g bmm bmm
COPY --from=build /server /app/server
USER bmm
EXPOSE 8000
ENTRYPOINT ["/app/server"]
CMD ["api"]
