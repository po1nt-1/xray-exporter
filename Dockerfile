FROM golang:1.26.3-alpine AS builder

ARG GIT_VERSION=dev
ARG GIT_COMMIT=none
ARG BUILD_DATE=unknown

ENV CGO_ENABLED=0 GOCACHE=/root/.cache/go-build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags "-s -w \
	-X main.buildVersion=${GIT_VERSION} \
	-X main.buildCommit=${GIT_COMMIT} \
	-X main.buildDate=${BUILD_DATE}" \
	-o /xray-exporter .

FROM scratch

ARG GIT_VERSION=dev
ARG GIT_COMMIT=none
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="xray-exporter" \
	org.opencontainers.image.description="Prometheus exporter for Xray/V2Ray metrics" \
	org.opencontainers.image.source="https://github.com/po1nt-1/xray-exporter" \
	org.opencontainers.image.version=${GIT_VERSION} \
	org.opencontainers.image.revision=${GIT_COMMIT} \
	org.opencontainers.image.created=${BUILD_DATE} \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.base.name="gcr.io/distroless/static:nonroot"

COPY --from=builder /xray-exporter /xray-exporter
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/nsswitch.conf /etc/nsswitch.conf

EXPOSE 9550

USER 65532:65532

ENTRYPOINT ["/xray-exporter"]
