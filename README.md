# AI Receptionist Orchestrator

Runs the whole voice pipeline from a single command, shows the **time delays between
each stage in milliseconds** live, and writes a per-session latency report.

```
mic ─► STT-system ─► /tmp/stt-system.sock ─► LLM-System ─► /tmp/llm-system.sock ─► TTS-System ─► speaker
        (GPT-4o RT)                          (decision+Gemini)                     (ElevenLabs)
                       every stage ──emits MetricEvent(utt_id,stage,ts)──► /tmp/orch-metrics.sock ─► orchestrator
```

The orchestrator is a **process supervisor**: it launches STT, LLM and TTS as headless
children, owns the terminal with a live dashboard, collects each stage's timing over a
Unix **metrics socket**, joins them per utterance by a shared `utt_id`, and on exit prints
+ persists a report. Policy is **fail-fast**: if any child dies, the others are stopped and
the run ends with the cause.

## Layout

```
AI-Receptionist-Orcastrator/
├── shared/                 # github.com/ai-receptionist/shared — imported by all 4 repos
│   ├── wire/               #   Event, Command, UttID  (the socket schemas + correlation id)
│   └── metrics/            #   MetricEvent, Emitter (child side), Listener (orchestrator side)
├── cmd/orchestrator/       # entrypoint: supervise → collect → dashboard → report
└── internal/
    ├── config/  supervisor/  collect/  stats/  tui/  report/
../go.work                  # ties shared + the 4 repos together
```

`utt_id` originates at STT's VAD speech-start and rides every stream event, every
LLM→TTS command, and every MetricEvent, so one utterance's STT/LLM/TTS timings join into
one end-to-end number. The old per-repo `metrics.jsonl` writers are gone — metrics now flow
to the orchestrator's socket (or are silently dropped when run standalone).

## Prerequisites

- Go 1.26.3 and the root `go.work` (already present at `/Users/aa/Documents/Projects/go.work`).
- A microphone and speaker.
- Each child's own `.env` filled (kept per-repo, by design):
  - `STT-system/.env` → `OPENAI_API_KEY` (+ optional `STT_LANGUAGE=tr`)
  - `LLM-System/.env`  → `GEMINI_API_KEY` (+ optional `GEMINI_TEXT_MODEL`)
  - `TTS-System/.env`  → `ELEVENLABS_API_KEY`, `ELEVENLABS_VOICE_ID`
- STT's embedding sidecar venv present (`STT-system/internal/category/embedding/.venv`).

## Run (live mic + speaker)

```sh
cd /Users/aa/Documents/Projects/AI-Receptionist-Orcastrator
go run ./cmd/orchestrator
```

You'll see startup lines (`metrics socket up`, `stt: ready`, `llm: ready`, `tts: ready` —
STT can take a while on first run while the embedding model loads), then the dashboard:

```
● orchestrator   stt:ready  llm:ready  tts:ready   utt 7  ok 6  no-speak 1  err 0
┌ Recent Utterances ───────────────────────┐ ┌ Aggregates (rolling) ──────────┐
 utt        stt   dec    gem  1stB   play  e2e   stage          n   p50   p95  max
 #7         410    12    480    90    140 1130   STT final      7   205   260  410
 #6         190    10     —     —      —    —     LLM decision   7    11    18   40
 ...                                            LLM gemini     5   470   720 1180
                                                TTS 1st byte   6    88   140  210
                                                TTS played     6   135   190  300
                                                END-TO-END     6  1100  1450 2100
```

Each row is one utterance: `stt` = speech-start→final, `dec` = final→LLM decision,
`gem` = Gemini call, `1stB`/`play` = TTS first-byte / first-audio (from command arrival),
`e2e` = speech-start→first audio audible. Cells are color-coded by latency.

**Stop** with `Ctrl-C` (or `q`). The TUI exits, the report prints to the restored terminal,
and files land in `runs/<timestamp>/`:
- `report.txt`  — the per-stage table (min/avg/p50/p95/max over the whole session)
- `report.json` — the same, machine-readable
- `metrics.jsonl` — every raw `MetricEvent` (the unified replacement for the old per-repo files)

### Flags
`--projects-root <dir>` (default: parent of CWD) · `--metrics-sock <path>` ·
`--prebuilt` (run `./bin/<name>` instead of `go run`) · `--ready-timeout 90s` · `--stop-grace 5s`

### Fail-fast example
Start with a bad `ELEVENLABS_API_KEY` → TTS dies → STT+LLM are stopped → the report shows
`exit: fail_fast, failed_child: tts` with the child's stderr tail, and the process exits non-zero.

## Test

Deterministic tests (no APIs, no mic) cover the orchestrator's core and the cross-system
metrics path:

```sh
# from /Users/aa/Documents/Projects
go test ./...        # in each module; or per-module:
( cd AI-Receptionist-Orcastrator && go test ./... )   # join math, stats percentiles,
                                                       # Emitter↔Listener socket round-trip,
                                                       # supervisor launch/fail-fast/env
( cd LLM-System && go test ./... )                     # decision engine + metric adapter over the socket
```

The full live mic→speaker run (above) is the end-to-end acceptance test; verify a single
`utt_id` spans `stt.*`/`llm.*`/`tts.*` lines in `runs/<ts>/metrics.jsonl`.
