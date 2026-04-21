# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.1

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -ldflags="-s -w -X lhotse-agent/cmd/mgr.Version=${VERSION}" \
    -o /out/lhotse-agent \
    .

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -o /out/lhotse-iptables \
    ./iptables

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -o /out/lhotse-clean-iptables \
    ./clean-iptables

FROM alpine:3.20

RUN apk add --no-cache ca-certificates iproute2 iptables

COPY --from=build /out/lhotse-agent /usr/local/bin/lhotse-agent
COPY --from=build /out/lhotse-iptables /usr/local/bin/lhotse-iptables
COPY --from=build /out/lhotse-clean-iptables /usr/local/bin/lhotse-clean-iptables

ENTRYPOINT ["/usr/local/bin/lhotse-agent"]
