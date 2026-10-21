# Local debug image. The mini-PC layout in the README is what runs on site.
FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mediateca-audio-station ./cmd/audio-station

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates mpv \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/mediateca-audio-station /usr/local/bin/mediateca-audio-station
COPY docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY docker/pipewire-client.conf /usr/share/pipewire/client.conf
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh \
    && install -d -m 0700 /var/lib/mediateca-audio-station /etc/mediateca-audio-station \
    && install -d -m 0700 /root/.config/pulse

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
