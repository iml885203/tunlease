FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunlease ./cmd/tunlease && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/tunlease-testapp ./cmd/testapp

# One binary, selected by subcommand. The gateway and sidecar targets share the
# same image and differ only by their default command.
FROM gcr.io/distroless/static-debian12:nonroot AS gateway
COPY --from=build /out/tunlease /tunlease
ENTRYPOINT ["/tunlease"]
CMD ["gateway"]

FROM gcr.io/distroless/static-debian12:nonroot AS sidecar
COPY --from=build /out/tunlease /tunlease
ENTRYPOINT ["/tunlease"]
CMD ["sidecar"]

FROM gcr.io/distroless/static-debian12:nonroot AS testapp
COPY --from=build /out/tunlease-testapp /tunlease-testapp
ENTRYPOINT ["/tunlease-testapp"]

FROM gateway
