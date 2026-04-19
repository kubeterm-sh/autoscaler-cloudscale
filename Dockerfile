FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown

RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w \
      -X github.com/kubeterm-sh/autoscaler-cloudscale/pkg/version.Version=${VERSION} \
      -X github.com/kubeterm-sh/autoscaler-cloudscale/pkg/version.Commit=${COMMIT} \
      -X github.com/kubeterm-sh/autoscaler-cloudscale/pkg/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /autoscaler-cloudscale ./cmd/autoscaler-cloudscale/

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /autoscaler-cloudscale /autoscaler-cloudscale

USER 65534:65534

ENTRYPOINT ["/autoscaler-cloudscale"]
