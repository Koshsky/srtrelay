# srtrelay

`srtrelay` is a small Go utility that accepts one incoming SRT stream and fans it out to multiple outgoing SRT listeners. All endpoints work in listener mode.

## Build

```bash
go build -o ./bin/srtrelay ./cmd/srtrelay
```

Or with Make:

```bash
make build
```

## Project Layout

```text
.
├── cmd/
│   └── srtrelay/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go
│   └── relay/
│       └── relay.go
├── go.mod
├── go.sum
└── README.md
```

## Run

Example: one publisher on `:9000`, two listener outputs on `:9001` and `:9002`.

```bash
go run ./cmd/srtrelay \
  -input-addr :9000 \
  -input-streamid source \
  -output :9001,view1 \
  -output :9002,view2
```

Or with Make:

```bash
make run ARGS='-input-addr :9000 -input-streamid source -output :9001,view1 -output :9002,view2'
```

Output format:

```text
-output addr[,streamid[,passphrase]]
```

If `streamid` is omitted, that listener accepts any stream id. If `passphrase` is omitted, encryption is disabled for that endpoint.

By default `-write-timeout` is `0` (disabled), which is usually safer for OBS subscribers that can pause/reopen sources.

## Test With ffmpeg and ffplay

Start the relay:

```bash
go run ./cmd/srtrelay \
  -input-addr :9000 \
  -input-streamid source \
  -output :9001,view1 \
  -output :9002,view2

# Optional: explicit no-deadline mode for OBS-heavy scenarios
# -write-timeout 0
```

Publish one SRT input into the relay:

```bash
ffmpeg -re -stream_loop -1 -i sample.mp4 \
  -c:v libx264 -preset veryfast -tune zerolatency \
  -c:a aac \
  -f mpegts \
  "srt://127.0.0.1:9000?mode=caller&streamid=source"
```

Connect two independent viewers to different outputs:

```bash
ffplay "srt://127.0.0.1:9001?mode=caller&streamid=view1"
```

```bash
ffplay "srt://127.0.0.1:9002?mode=caller&streamid=view2"
```

You can also use `ffmpeg` instead of `ffplay` as a sink for validation:

```bash
ffmpeg -i "srt://127.0.0.1:9001?mode=caller&streamid=view1" -f null -
```