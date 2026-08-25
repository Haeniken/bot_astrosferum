FROM golang:1.27.0-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot_astrosferum ./cmd/bot_astrosferum \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot_astrosferum_directional_worker ./cmd/bot_astrosferum_directional_worker

FROM ghcr.io/osgeo/gdal:ubuntu-small-3.13.3

ARG APP_UID=1000
ARG APP_GID=1000

RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        cdo \
        libeccodes-tools \
        netcdf-bin \
        tzdata \
    && rm -f /usr/bin/pebble \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/bot_astrosferum /usr/local/bin/bot_astrosferum
COPY --from=builder /out/bot_astrosferum_directional_worker /usr/local/bin/bot_astrosferum_directional_worker

USER ${APP_UID}:${APP_GID}
ENTRYPOINT ["/usr/local/bin/bot_astrosferum"]
CMD ["help"]
