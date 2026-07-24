FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunlease-gateway ./cmd/gateway && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunlease-sidecar ./cmd/sidecar && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunlease ./cmd/cli && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunlease-testapp ./cmd/testapp

FROM gcr.io/distroless/static-debian12:nonroot AS gateway
COPY --from=build /out/tunlease-gateway /tunlease-gateway
ENTRYPOINT ["/tunlease-gateway"]

FROM gcr.io/distroless/static-debian12:nonroot AS sidecar
COPY --from=build /out/tunlease-sidecar /tunlease-sidecar
ENTRYPOINT ["/tunlease-sidecar"]

FROM gcr.io/distroless/static-debian12:nonroot AS testapp
COPY --from=build /out/tunlease-testapp /tunlease-testapp
ENTRYPOINT ["/tunlease-testapp"]

FROM gateway
