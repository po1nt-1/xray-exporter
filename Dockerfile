FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
	-ldflags "-s -w -X main.buildVersion=${VERSION} -X main.buildCommit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
	-o /xray-exporter .

FROM scratch

ARG VERSION=dev
LABEL org.opencontainers.image.title="xray-exporter" \
	org.opencontainers.image.description="Prometheus exporter for Xray-core" \
	org.opencontainers.image.source="https://github.com/po1nt-1/xray-exporter" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.licenses="MIT"

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /xray-exporter /xray-exporter
EXPOSE 9550
USER 65532:65532
ENTRYPOINT ["/xray-exporter"]
CMD ["--listen", ":9550"]
