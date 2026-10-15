# syntax=docker/dockerfile:1
# Both programs cross-compile on the build machine, so a multi-arch build runs
# only the final stage's mkdir under emulation.
FROM --platform=$BUILDPLATFORM golang:1.26.9-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/burrow .

# burrow-tether, for Settings > Reach from anywhere: a static musl binary
FROM --platform=$BUILDPLATFORM rust:1-alpine AS tether
ARG TARGETARCH
RUN apk add --no-cache musl-dev zig \
 && cargo install --locked cargo-zigbuild@0.23.4
WORKDIR /src
COPY tether/Cargo.toml tether/Cargo.lock ./
COPY tether/src ./src
# a cache per architecture: the amd64 and arm64 builds run at once
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=cargo-registry-$TARGETARCH \
    --mount=type=cache,target=/usr/local/cargo/git,id=cargo-git-$TARGETARCH \
    --mount=type=cache,target=/src/target,id=tether-$TARGETARCH \
    T=$([ "$TARGETARCH" = arm64 ] && echo aarch64 || echo x86_64)-unknown-linux-musl \
 && rustup target add $T \
 && cargo zigbuild --locked --release --target $T \
 && cp target/$T/release/burrow-tether /burrow-tether

FROM alpine:3.22
RUN mkdir -p /data /drop /state && chmod 777 /drop /state
COPY --from=build /out/burrow /usr/local/bin/burrow
COPY --from=tether /burrow-tether /usr/local/bin/burrow-tether
ENV BURROW_ADDR=:47823 BURROW_PUBLIC_ADDR=:47824 BURROW_DATA=/data BURROW_DROP=/drop BURROW_STATE=/state
# 47823: the whole app, for your LAN. 47824: share links only, for your reverse proxy. Both HTTPS only.
EXPOSE 47823 47824
# not root by default; compose and the TrueNAS template set the files' owner instead
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s CMD ["burrow", "healthcheck"]
ENTRYPOINT ["burrow"]
