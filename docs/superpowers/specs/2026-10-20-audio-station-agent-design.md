# Audio station agent — design

## What this is

A **new repository**, `mediateca-audio-station`, implements an audio-only
station agent for Linux mini-PCs. It pulls the Hub’s daily timed playlist,
caches media files locally, and plays them with **mpv**
([mpv.io](https://mpv.io)) on the machine itself.

The Hub registers exactly one **pseudo-screen** on an audio station (see
`2026-10-20-station-medium-audio-video-design.md`). The agent maps that
`screen_id` to the local audio output. There is no Android TV, no ADB, and no
LAN HTTP media server for displays.

The Broadcast Hub in `mediateca-broadcast` is not modified by this repository
beyond the medium/playlist work described in the Hub design. This agent
consumes the **existing** agent API.

A Ruby/Spinel prototype lives in `mediateca-audio-station-ruby`. Spinel cannot
compile it: the compiler snapshot lacks `YAML`, `JSON`, `Net::HTTP`, `URI`,
`FileUtils`, `Data.define`, and `Process.spawn`. That tree is an archive. This
document replaces it.

## Why this shape

Video stations already use a Go agent (`mediateca-station`) that drives VLC on
Android TVs over ADB. Audio stations have a different physical topology: the
mini-PC **is** the player. A second agent keeps that difference explicit and
avoids loading ADB/VLC concerns into the audio path.

Chosen runtime: **one Go binary**, the same shape as `mediateca-station`
(module, `internal/` packages, `cmd/`, systemd, `go test`). Host `mpv` plays
the cached file. Rejected: extending the video agent with an mpv mode (crosses
the product split: screens, ADB, and LAN HTTP do not belong on an audio
mini-PC). Rejected: a custom audio API on the Hub (duplicates v2 timed
playlist). Rejected: Ruby compiled with Spinel (the libraries this agent needs
are not in the compiler). Rejected: bundling mpv inside the Go binary (use the
host package).

## Decisions already made

- **Process.** One long-running Go binary, module `mediateca-audio-station`,
  Go 1.22 or newer. systemd `Restart=always`. A panic exits the process;
  systemd brings it back. The binary stays up when the screen is idle or the
  Hub is down. It exits non-zero only when the config file is missing,
  unreadable, invalid, or world/group-readable (`0077` bits set), or when
  `mpv_path` cannot be found, so a bad config does not crash-loop quietly.
  Runtime playback errors do not exit the process.
- **Layout.** Match `mediateca-station`: `cmd/audio-station` parses `-config`
  and calls `agent.Run`. Packages under `internal/` each have one job.
  `log/slog` on stdout. `gopkg.in/yaml.v3` for the config file. Standard
  library HTTP. No other module dependencies.
- **Hub base.** Config `hub_base_url` (no trailing path). Paths appended:
  `/api/agent/v1/config`, `/api/agent/v2/package`,
  `/api/agent/v1/play_events`.
- **Auth.** `Authorization: Bearer <agent_token>` on `/api/agent/*` only. Media
  downloads do not send the token (same as the video agent). Redirects strip
  `Authorization`. Follow at most five redirects.
- **Clocks.** Mini-PC clock + NTP (chrony or equivalent) in UTC. Schedule uses
  absolute `starts_at` instants from the package. Location TZ is not
  reinterpreted on the agent.
- **Late threshold.** Default 2 seconds (`late_threshold`), a Go duration.
- **Poll.** Default 1 minute (`poll_interval`), a Go duration. Each poll
  refreshes config and package.
- **Screen binding.** Config names the single Hub `screen_id`. The agent does
  not play any other id. If the stored Hub config lists zero screens, or lists
  screens and omits this id, log once per change and do not play. If the Hub
  lists several screens and this id is among them, play only this id and log
  once that the list is not a single screen. When no Hub config has ever been
  stored, the local `screen_id` is the allow-list, so the first boot can play
  after the first package arrives.
- **Player.** Host `mpv` binary (`mpv_path`, default `mpv`). Argument vector,
  not a shell string:
  `mpv --no-video --no-terminal --really-quiet [--audio-device=<device>] -- <path>`.
  Optional `audio_device` is omitted when empty. **MVP stop/start:** signal
  the previous mpv process (`SIGTERM`, then `SIGKILL` after 2s) and spawn the
  next. No long-lived IPC session.
- **Media.** Play the cached original file from the package URL (audio MIME).
  No local transcode. The scheduler passes the local filesystem path to mpv.
- **Play events.** Enqueue only after mpv **start succeeds** (process spawned),
  with the timestamp taken immediately before the start attempt (same
  reporting semantics as the video agent’s pre-ADB timestamp). A failed spawn
  records nothing and does not add the played key, so a later tick inside the
  late window may try again.
- **Schedule rules.** Same timeline rules as `mediateca-station` for the one
  screen: late window, played-set, conflict by smaller `position`, stop at
  `starts_at + duration`, no stop between clips joined within 1 second (the
  next start replaces mpv), missing file waits inside the window and is
  skipped after it. See Schedule below.
- **Secrets on disk.** Refuse start if config mode allows group/world access.
  Never log the token or the `Authorization` header.
- **Out of MVP.** Multi-channel audio, CEC/display control, companion UI,
  Hub API changes, audio transcode, heartbeat routes, frame sync, LAN HTTP
  media serving, ADB, extending `mediateca-station`.

## Components

Entry point parses `-config` and calls `Run`. Each package has one job.

| Package | Responsibility |
| --- | --- |
| `internal/fsutil` | Temp file plus rename. |
| `internal/config` | Load and validate YAML. Enforce file mode. Defaults. |
| `internal/hub` | HTTP client: config, package, one play-event POST. |
| `internal/cache` | Download `media.url` by `media.id`; prune unreferenced files. |
| `internal/schedule` | Pure timeline: given entries, screen id, and a clock, what should happen. |
| `internal/player` | Spawn and kill mpv for a local path. No scheduling policy. |
| `internal/report` | Durable queue of play events and the played-set. |
| `internal/agent` | Wire poll, cache, schedule, play, and report. |
| `cmd/audio-station` | `-config` flag and process exit. |

`internal/schedule` and `internal/report` do not import the player, the hub,
or the cache. Tests of those two packages need no network and no mpv.

### Config file

Path from `-config` (README uses
`/etc/mediateca-audio-station/config.yaml`).

```yaml
hub_base_url: "https://hub.example"
agent_token: "<token issued once by the hub>"
data_dir: "/var/lib/mediateca-audio-station"
screen_id: 42
late_threshold: "2s"
poll_interval: "1m"
mpv_path: "mpv"
# audio_device: "alsa/hdmi:CARD=PCH,DEV=0"
```

`late_threshold` and `poll_interval` are Go durations (`time.ParseDuration`).
Missing optional durations mean 2s and 1m. Missing `mpv_path` means `mpv`.
`audio_device` is optional.

Validation:

- `hub_base_url` required, absolute `http` or `https`, host present, no path
  other than empty or `/`. Strip one trailing `/` before use.
- `agent_token` non-empty.
- `data_dir` non-empty. Create it `0700` when the process starts, if missing.
- `screen_id` positive integer.
- `poll_interval` positive. `late_threshold` zero or positive.
- `mpv_path` non-empty. `exec.LookPath` (or `os.Stat` when the value contains
  a path separator) must succeed at startup. Failure exits non-zero.
- Config file mode must not have any bit in `0077`.

### On-disk state

Under `data_dir`, created `0700` if missing. Same names as the video agent,
without an HTTP secret:

| Path | Contents |
| --- | --- |
| `config.json` | Last HTTP 200 config body, written by temp file plus rename. |
| `package.json` | Last HTTP 200 package body, written by temp file plus rename. |
| `package.etagheader` | Raw `ETag` response header, sent back as `If-None-Match`. |
| `media/<id>` | Complete file for that `media.id`. |
| `media/<id>.url` | The package `media.url` string that produced the file. |
| `media/<id>.mime` | The `mime_type` string. |
| `media/<id>.partial` | In-progress download. Never played. |
| `events.jsonl` | Queued play events, one JSON object per line. |
| `played.json` | Keys `screen_id:media_id:starts_at` already handed to the queue. Rewritten by temp file plus rename. |

`starts_at` in the played key is the RFC3339 UTC string from the package, not
the command timestamp. `package.etagheader` is the raw header so a restart can
send the Rails value unchanged. Do not rebuild it from the JSON `etag` field.

## Hub contract the agent depends on

Same contract as `mediateca-station`. The hub authenticates the bearer token
to one station. Missing, malformed, and unknown tokens are HTTP 401:

```json
{ "error": "unauthorized" }
```

### GET `/api/agent/v1/config`

HTTP 200:

```json
{
  "station_id": 3,
  "offline_cache_hours": 24,
  "screens": [
    { "id": 42, "name": "Зал", "orientation": "landscape" }
  ]
}
```

`offline_cache_hours` is the hub's horizon. The agent does not compute a
second horizon from it. It plays the entries of the package it has stored.
The value is logged. An audio station has one pseudo-screen; extra screens
are a Hub misconfiguration and are only logged.

### GET `/api/agent/v2/package`

HTTP 200 with an `ETag` header and `Cache-Control: private, must-revalidate`.
The agent stores the raw `ETag` header value and sends it back as
`If-None-Match`. A matching `If-None-Match` yields HTTP 304 and an empty
body. The agent keeps the previously stored package.

HTTP 200 body (timed entries, never the v1 `items` shape):

```json
{
  "version": "sha256",
  "etag": "sha256",
  "generated_at": "2026-09-02T12:00:00Z",
  "valid_until": "2026-09-03T12:00:00Z",
  "entries": [
    {
      "for_date": "2026-09-02",
      "broadcast_day_starts_at": "2026-09-02T02:00:00Z",
      "position": 1,
      "offset_seconds": 0,
      "starts_at": "2026-09-02T02:00:00Z",
      "duration_seconds": 10,
      "source_kind": "service",
      "media_plan_id": null,
      "screen_ids": [42],
      "media": {
        "id": 19,
        "url": "/rails/active_storage/blobs/redirect/...",
        "mime_type": "audio/mpeg"
      }
    }
  ],
  "screen_map": { "42": [0] }
}
```

The agent schedules from `entries[].screen_ids`. It does not read
`screen_map`. It does not branch on `source_kind` or `media_plan_id`.

`media.url` is a path, joined to `hub_base_url`. It is an Active Storage
signed redirect and authorizes itself. The downloader follows up to five
redirects and does not attach the bearer token. Audio URLs point at the
original upload. The agent does not look for an MPEG-TS sibling.

An entry is ignored, and an error is logged, when `starts_at` is missing,
`duration_seconds` is less than or equal to zero, or `media.id` / `media.url`
/ `media.mime_type` is missing.

`valid_until` is not an extra stop. Playback ends when the stored entries for
the configured screen have been started or skipped. An empty `entries` array
means the screen is stopped.

### POST `/api/agent/v1/play_events`

One event per request, so a rejection cannot discard a neighbor:

```json
{
  "events": [
    {
      "screen_id": 42,
      "media_asset_id": 19,
      "started_at": "2026-09-02T02:00:01Z"
    }
  ]
}
```

`started_at` is UTC RFC3339. Success is HTTP 201:

```json
{ "play_log_ids": [101] }
```

HTTP 404 is `{ "error": "not_found" }` when the screen is not on this station
or the hub will not attribute the media. HTTP 422 is
`{ "error": "invalid_record", "details": ["..."] }` when the record is
invalid. The hub applies the whole `events` array or none of it; a
one-element array keeps that from mattering.

## Data flow

1. On startup, load `config.json`, `package.json`, and `package.etagheader`
   when present, then start the reporter and the scheduler. Do not wait for a
   live hub before playing a stored package. The screen plays only when its
   id is the configured `screen_id` and, once a hub config has been stored,
   that id is present in the latest stored hub config.
2. Each poll: GET config, then GET package with the stored `If-None-Match`.
3. HTTP 304 leaves the stored package and its files alone.
4. HTTP 200 from config replaces `config.json` only after the body parses.
   HTTP 200 from the package replaces `package.json` and `package.etagheader`
   only after the body parses. The scheduler swaps in the new timeline before
   the cache deletes anything. The cache then downloads any `media.id` whose
   stored `.url` differs from the new `media.url`, and deletes stored ids that
   the new package does not reference. Deletion happens only after a
   successful package 200, never while the hub is down. A file whose `.url`
   does not match the current package URL is not ready, even if `media/<id>`
   still exists from the previous URL. The old bytes stay until the new
   download is renamed into place.
5. Downloads run at most two at a time. Bytes go to `media/<id>.partial` and
   are renamed to `media/<id>` only when the response is complete (HTTP 200
   and the body fully read). After that rename, the cache writes `.url` and
   `.mime`. A crash between the rename and the sidecars leaves the id not
   ready, so the next poll downloads it again. A partial file is never
   selected by the scheduler.
6. mpv is given the absolute path of `media/<id>`. There is no LAN URL.
7. The reporter drains `events.jsonl` independently of the scheduler.

## Schedule

Copied from the video agent, applied to the single configured screen.

For that screen, the timeline is the entries that were not ignored and whose
`screen_ids` contain that screen, ordered by `starts_at` ascending, then by
`position` ascending. Two entries with the same `starts_at` are a conflict:
the smaller `position` is the one that may play, and the others are logged
and skipped.

The scheduler sleeps until the next start, stop, or late-threshold instant.
A package swap wakes it immediately. While a clip is inside its late window
and the file is not ready yet, the scheduler wakes again in 200ms.

Let `now` be the mini-PC clock and `late_threshold` the configured duration.

**Start.** When `now` reaches `starts_at`, start the clip if all of the
following hold:

- the played-set does not already contain `screen_id:media_id:starts_at`;
- `now <= starts_at + late_threshold`;
- `media/<id>` is complete and its `.url` sidecar equals this entry's
  `media.url`;
- this entry is the conflict winner for that instant;
- the screen is allowed (see Screen binding).

The player stops any current mpv, then spawns the next. `started_at` in the
event is the UTC time sampled immediately before spawn. The event is appended
and the played key is recorded only when spawn succeeds. A spawn failure
records nothing and does not add the played key.

**Skip.** If the scheduler first observes the entry when
`now > starts_at + late_threshold`, it does not start it and does not report
it. A missing file after the window is the same skip, with reason
`missing_file`. A process restart in the middle of a clip is the late case:
that clip is not resumed, and the screen waits for the next `starts_at`.

**Stop.** The clip's end is `starts_at + duration_seconds`. If the next clip
on that screen has `starts_at <= end + 1s`, the scheduler does not stop first;
it issues the next start at the next `starts_at` (subject to the start
rules). The new spawn replaces playback. If that next start fails, the player
stops mpv and still does not report the failed start. If the gap to the next
`starts_at` is longer than 1 second, or there is no next clip, the player
stops mpv at `end`.

The agent remembers the entry key it last started successfully. That memory
is the clip on the output. It is process memory only; a restart does not
resume a clip.

**Interrupt.** After a package swap, look at the entry that covers `now`
(`starts_at <= now < starts_at + duration_seconds`). If its key differs from
the remembered key, or nothing covers `now`, stop mpv and clear the remembered
key. A covering entry starts only when it still satisfies the start rules,
including the late threshold measured from its own `starts_at`. Otherwise the
output stays silent until a future `starts_at`. A swap that keeps the same
covering key does not restart and does not stop.

**Idle.** No current entry means mpv is stopped. An empty package stops the
output on the next wake.

**Early exit.** If mpv exits before the scheduled stop, log it and wait for
the next schedule decision. Do not spawn the same clip again (the played key
is already set). Do not exit the process.

## Player commands

The player runs `mpv` with an argument vector, not a shell string. Start and
stop do not overlap: one mutex covers both.

Start:

```text
mpv --no-video --no-terminal --really-quiet --audio-device=<device> -- <absolute-path>
```

Omit `--audio-device=...` when `audio_device` is empty.

Stop: `SIGTERM` to the child, wait up to 2 seconds, then `SIGKILL`. A missing
child is success.

Spawn failure is a failed start. The scheduler continues.

## Reporter

The queue is `events.jsonl`. The reporter posts the first line, then:

| Result | Action |
| --- | --- |
| HTTP 201 | Remove that line. |
| HTTP 401 | Leave the queue. Pause posts until a later config or package poll returns 200 or 304. Playback continues. Log once that the token is rejected. |
| HTTP 404 or 422 | Drop that line and log the body. Do not retry. |
| Network error, timeout, or any other status | Leave the line. Retry on the next poll interval. Do not block playback. |

A poll that receives 401 does not replace the package or the config and does
not delete media. The reporter stays paused until a later poll of config or
package returns 200 or 304. The stored timeline keeps playing until its
entries are exhausted. The same is true for a network error or HTTP 5xx on
the poll. A failed download leaves the previous complete file in place when
the URL has not changed; when the URL changed, the old file stays until the
new one is complete, and the scheduler keeps treating the old file as not
ready for the new URL.

Disk full is a failed download. Already complete files keep playing.

## Logging

`log/slog` on stdout, which journald captures. Info: poll result, clip start,
clip stop, skip with a reason (`late`, `missing_file`, `conflict`,
`ignored_entry`). Error: mpv spawn failure, hub 401, dropped play event,
screen-list mismatch, mpv exit before the scheduled stop. Never log
`agent_token` or the `Authorization` header.

## Testing

`go test ./...` uses a fake clock, `httptest` as the hub and as the redirect
target, and a fake player that records the argument vector. No mpv binary and
no audio device are required. CI matches `mediateca-station`: `gofmt`,
`go vet`, `golangci-lint`, `govulncheck`, `go build ./...`, and
`go test -race -count=1 ./...`.

The suite covers:

- start when `now == starts_at`, and when `now == starts_at + late_threshold`;
- no start and no event when `now` is 1 nanosecond past the threshold;
- a restart mid-clip does not resume it;
- a failed mpv spawn inside the window may be retried; a successful one may
  not, including across a reload of `played.json`;
- two clips joined within 1 second do not stop between them;
- a longer gap stops at `end`;
- an entry whose `screen_ids` omit this screen never starts;
- the same `starts_at` plays only the smaller `position`;
- an empty `entries` array stops mpv;
- a new package whose current entry differs stops the old clip;
- a relative `media.url` is joined to `hub_base_url` and fetched without the
  bearer token, following a redirect;
- a `.partial` file is not played;
- a changed URL for the same `media.id` downloads again;
- an id missing from a new HTTP 200 package is deleted; it is kept when the
  poll fails;
- with no stored hub config, the configured screen may play; once
  `config.json` exists, a screen absent from it does not;
- reporter: 201 removes the line, 404 and 422 drop it, 401 and a network
  error keep it;
- config mode `0644` is refused; a missing `mpv_path` binary is refused;
- spawn argv contains `--no-video` and the cached path, and contains
  `--audio-device=` only when configured.

## systemd

Example unit (ship in repo `packaging/mediateca-audio-station.service`):

```ini
[Unit]
Description=Mediateca audio station
After=network-online.target chronyd.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/mediateca-audio-station -config /etc/mediateca-audio-station/config.yaml
Restart=always
RestartSec=2
User=mediateca-audio
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

`chronyd.service` may be `chrony.service` or `systemd-timesyncd.service` on a
given image. The README says to set `After=` and `Wants=` to the NTP unit that
image actually ships, and not to start the agent before the clock has stepped.

Host packages: `mpv`, an NTP client. The binary has no Ruby runtime and no
CGo requirement. Build with `CGO_ENABLED=0`.

## README the new repository ships

The README is the operator page for the mini-PC, not a hub guide. It states:

- the process is a client of an existing hub; it does not create playlists;
- install `mpv` from the distro and an NTP client;
- install the binary, write the config (mode `0600`), and install the systemd
  unit;
- the unit runs as `mediateca-audio`, which owns `data_dir`;
- how to check journald for `late`, `missing_file`, and token rejection;
- `screen_id` is the Hub pseudo-screen on this audio station.

## Relationship to the video agent

| Concern | Video (`mediateca-station`) | Audio (`mediateca-audio-station`) |
| --- | --- | --- |
| Language | Go | Go |
| Display | Android TV + VLC via ADB | Local mpv, `--no-video` |
| Media serve | LAN HTTP to TVs | Local filesystem path |
| Screens | Many, serial map | One `screen_id` |
| Hub API | v1 config, v2 package, play_events | Same |
| On-air file | MPEG-TS for video | Original audio upload |
| Schedule | `internal/schedule` timeline | Same rules, one screen |

## Out of scope

- Hub schema/UI (owned by Hub design).
- Changing Hub package JSON.
- Playing video on an audio station.
- Bundling mpv or ffmpeg into the binary.
- macOS/Windows as production targets (Linux mini-PC only for MVP).
- Porting the Ruby/Spinel tree forward. It stays in
  `mediateca-audio-station-ruby`.

## Done when

A new agent can implement `mediateca-audio-station` from this document alone.
`go test ./...` passes without mpv. The README is enough for an operator to
point a mini-PC at a hub token and one pseudo-screen. The hub repository gains
no runtime code.
