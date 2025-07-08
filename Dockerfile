FROM scratch

ARG TARGETARCH

EXPOSE 9550

COPY dist/xray-exporter-linux-${TARGETARCH} /usr/bin/xray-exporter

ENTRYPOINT [ "/usr/bin/xray-exporter" ]
