FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/qoder2api .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/qoder2api /app/qoder2api
# 3588 控制台 · 8963 国内版桥 · 8964 国际版桥
EXPOSE 3588 8963 8964
ENV HOME=/data
VOLUME ["/data"]
# 默认单进程双桥；只跑一侧时追加 --cn-port=0 或 --global-port=0
CMD ["/app/qoder2api", "--bind=0.0.0.0"]
