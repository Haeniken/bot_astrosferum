FROM golang:1.26.5-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot_astrosferum ./cmd/bot_astrosferum

FROM ghcr.io/osgeo/gdal:ubuntu-small-3.13.1

ARG APP_UID=1000
ARG APP_GID=1000

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        cdo \
        libeccodes-tools \
        tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/bot_astrosferum /usr/local/bin/bot_astrosferum

USER ${APP_UID}:${APP_GID}
ENTRYPOINT ["/usr/local/bin/bot_astrosferum"]
CMD ["help"]
