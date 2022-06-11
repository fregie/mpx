FROM golang:1.18.3-alpine3.16 AS builder
RUN apk --no-cache add ca-certificates git
WORKDIR /mpx
COPY . .
RUN CGO_ENABLED=0 go build -o output/mpx ./mpx-tunnel

FROM debian:stable-slim
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /mpx/output/mpx /mpx

ENV LISTEN_ADDR "0.0.0.0:5512"
ENV SERVER_ADDR ""
ENV TARGET_ADDR ""
ENV CONCURRENT_NUM 4

ENTRYPOINT [ "bash", "-c", "/mpx -listen=${LISTEN_ADDR} -server=${SERVER_ADDR} -target=${TARGET_ADDR} -p=${CONCURRENT_NUM}" ]