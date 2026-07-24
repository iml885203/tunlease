FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunle ./cmd/tunlease && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunlease-testapp ./cmd/testapp

# The gateway image runs the single tunle binary with the `gateway` subcommand.
FROM gcr.io/distroless/static-debian12:nonroot AS gateway
COPY --from=build /out/tunle /tunle
ENTRYPOINT ["/tunle"]
CMD ["gateway"]

FROM gcr.io/distroless/static-debian12:nonroot AS testapp
COPY --from=build /out/tunlease-testapp /tunlease-testapp
ENTRYPOINT ["/tunlease-testapp"]

FROM gateway
