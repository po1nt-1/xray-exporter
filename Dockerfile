FROM alpine:latest
RUN apk --no-cache add ca-certificates
ARG TARGETARCH
EXPOSE 9550
COPY dist/xray-exporter-linux-${TARGETARCH} /usr/bin/xray-exporter
ENTRYPOINT [ "/usr/bin/xray-exporter" ]
