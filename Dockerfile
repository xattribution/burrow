# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/burrow .

FROM alpine:3.22
RUN mkdir -p /data /drop /state && chmod 777 /drop /state
COPY --from=build /out/burrow /usr/local/bin/burrow
ENV BURROW_ADDR=:47823 BURROW_DATA=/data BURROW_DROP=/drop BURROW_STATE=/state
EXPOSE 47823
HEALTHCHECK --interval=30s --timeout=5s CMD ["burrow", "healthcheck"]
ENTRYPOINT ["burrow"]
