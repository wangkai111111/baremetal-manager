FROM golang:1.25-bookworm AS build
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server
FROM alpine:3.20
RUN apk add --no-cache ca-certificates ipmitool && addgroup -S bmm && adduser -S -G bmm bmm
COPY --from=build /server /app/server
USER bmm
EXPOSE 8000
ENTRYPOINT ["/app/server"]
CMD ["api"]
