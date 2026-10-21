#!/bin/sh
# Writes a mode-0600 config (the process rejects a group-readable file) and
# starts the audio station. A mounted config at STATION_CONFIG, or a file
# already present at the default path, is used as-is.
set -eu

config_path="${STATION_CONFIG:-/etc/mediateca-audio-station/config.yaml}"

if [ -f "$config_path" ]; then
  exec /usr/local/bin/mediateca-audio-station -config "$config_path"
fi

: "${AGENT_TOKEN:?set AGENT_TOKEN to the hub station token (Station#assign_agent_token!)}"
: "${SCREEN_ID:?set SCREEN_ID to the hub pseudo-screen id}"

case "$SCREEN_ID" in
  *[!0-9]* | "")
    echo "audio-station: SCREEN_ID must be a positive integer" >&2
    exit 1
    ;;
esac
if [ "$SCREEN_ID" -eq 0 ]; then
  echo "audio-station: SCREEN_ID must be a positive integer" >&2
  exit 1
fi

# Development HostAuthorization allows IP literals and rejects the Compose
# name "web" (403). Leave HUB_BASE_URL unset so the station calls the hub
# container by the address it has on mediateca_broadcast_default.
if [ -n "${HUB_BASE_URL:-}" ]; then
  hub_base_url="$HUB_BASE_URL"
else
  hub_ip=$(getent ahostsv4 web | awk '{ print $1; exit }')
  if [ -z "$hub_ip" ]; then
    echo "audio-station: cannot resolve hub service web." >&2
    echo "audio-station: start Broadcast Hub first (mediateca-broadcast: docker compose up)." >&2
    exit 1
  fi
  hub_base_url="http://${hub_ip}:3000"
fi

poll_interval="${POLL_INTERVAL:-15s}"
late_threshold="${LATE_THRESHOLD:-2s}"
data_dir="${DATA_DIR:-/var/lib/mediateca-audio-station}"
mpv_path="${MPV_PATH:-mpv}"
audio_device="${AUDIO_DEVICE:-}"

# Pulse is optional. Without the host socket, mpv falls back to ALSA on /dev/snd.
if [ ! -S "${PULSE_SOCKET:-/run/host-pulse/native}" ]; then
  unset PULSE_SERVER || true
fi

yaml_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

install -d -m 0700 "$(dirname "$config_path")" "$data_dir"
umask 077
{
  printf 'hub_base_url: "%s"\n' "$(yaml_escape "$hub_base_url")"
  printf 'agent_token: "%s"\n' "$(yaml_escape "$AGENT_TOKEN")"
  printf 'data_dir: "%s"\n' "$(yaml_escape "$data_dir")"
  printf 'screen_id: %s\n' "$SCREEN_ID"
  printf 'late_threshold: "%s"\n' "$(yaml_escape "$late_threshold")"
  printf 'poll_interval: "%s"\n' "$(yaml_escape "$poll_interval")"
  printf 'mpv_path: "%s"\n' "$(yaml_escape "$mpv_path")"
  if [ -n "$audio_device" ]; then
    printf 'audio_device: "%s"\n' "$(yaml_escape "$audio_device")"
  fi
} > "$config_path"

exec /usr/local/bin/mediateca-audio-station -config "$config_path"
