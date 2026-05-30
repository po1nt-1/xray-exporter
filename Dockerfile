FROM golang@sha256:91eda9776261207ea25fd06b5b7fed8d397dd2c0a283e77f2ab6e91bfa71079d AS builder

ARG SOURCE_DATE_EPOCH=0

ENV CGO_ENABLED=0 \
	GOCACHE=/root/.cache/go-build

RUN rm -rf /root/.cache/go-build/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags "-s -w" -o /xray-exporter .

RUN sha256sum /xray-exporter

FROM scratch

ARG SOURCE_DATE_EPOCH=0

LABEL org.opencontainers.image.title="xray-exporter" \
	org.opencontainers.image.description="Prometheus exporter for Xray/V2Ray metrics" \
	org.opencontainers.image.source="https://github.com/po1nt-1/xray-exporter" \
	org.opencontainers.image.version=${SOURCE_DATE_EPOCH} \
	org.opencontainers.image.created=@${SOURCE_DATE_EPOCH} \
	org.opencontainers.image.licenses="MIT"

COPY --from=builder /xray-exporter /xray-exporter
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/nsswitch.conf /etc/nsswitch.conf

EXPOSE 9550

USER 65532:65532

ENTRYPOINT ["/xray-exporter"]
