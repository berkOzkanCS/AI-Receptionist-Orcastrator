// Package metrics defines the per-stage timing record that every pipeline
// system emits to the orchestrator over a Unix metrics socket (replacing each
// system's old per-repo metrics.jsonl), plus the non-blocking Emitter client
// and the orchestrator-side Listener. Every MetricEvent is tagged with the
// utterance's UttID and a Stage, so the orchestrator can reconstruct the full
// chain — speech-start -> STT final -> LLM decision -> Gemini -> TTS first byte
// -> audio played — for one utterance.
package metrics

import "github.com/ai-receptionist/shared/wire"

// DefaultPath is the orchestrator-owned metrics socket. Children dial it; the
// orchestrator listens. Absent (standalone run) => the Emitter silently drops.
const DefaultPath = "/tmp/orch-metrics.sock"

// EnvSocket is the environment variable the orchestrator sets when spawning a
// child to point its Emitter at the metrics socket.
const EnvSocket = "ORCH_METRICS_SOCK"

// Stage is the canonical pipeline checkpoint name. The "system." prefix records
// which process emitted it.
type Stage string

const (
	// STT
	StageSpeechStart Stage = "stt.speech_start" // VAD speech_started; utt_id minted
	StageSTTPartial  Stage = "stt.partial"      // first partial of an utterance
	StageSTTFinal    Stage = "stt.final"        // committed transcript

	// LLM
	StageLLMRegexHit     Stage = "llm.regex_hit"
	StageLLMEmbeddingHit Stage = "llm.embedding_hit"
	StageLLMDecision     Stage = "llm.decision"      // engine emitted a filler/answer
	StageLLMGeminiStart  Stage = "llm.gemini_start"       // Gemini call dispatched
	StageLLMFirstToken   Stage = "llm.gemini_first_token" // first streamed answer token (output start)
	StageLLMGemini       Stage = "llm.gemini"             // Gemini verify/answer returned (output end)
	StageLLMEmit         Stage = "llm.emit"          // speak command pushed to socket
	StageLLMDropped      Stage = "llm.dropped"       // speak dropped (queue full / no catalog)

	// TTS
	StageTTSArrival   Stage = "tts.arrival"    // command read off the socket
	StageTTSDial      Stage = "tts.dial"       // ElevenLabs WS connected
	StageTTSFirstByte Stage = "tts.first_byte" // first PCM frame off the wire
	StageTTSPlayed    Stage = "tts.played"     // first audio audible
	StageTTSFinal     Stage = "tts.final"      // synthesis finished
	StageTTSDropped   Stage = "tts.dropped"    // queued behind an interrupt / error
	StageTTSInterrupt Stage = "tts.interrupt"  // barge-in: received -> silenced
)

// MetricEvent is one JSON-line on the metrics socket. DeltaMs is the
// stage-local duration the emitter measured (e.g. Gemini call ms, or a TTS
// stage gap); TsMs is the wall-clock instant used to join stages across
// processes. Most fields are omitempty so each line shows only what matters.
type MetricEvent struct {
	UttID   wire.UttID `json:"utt_id"`
	Stage   Stage      `json:"stage"`
	System  string     `json:"system"`            // "stt" | "llm" | "tts"
	TsMs    int64      `json:"ts_ms"`             // wall-clock unix millis
	DeltaMs float64    `json:"delta_ms,omitempty"`// stage-local duration if meaningful

	Seq       uint64  `json:"seq,omitempty"`       // TTS backend lockstep seq
	Kind      string  `json:"kind,omitempty"`      // filler|answer|llm
	Category  string  `json:"category,omitempty"`
	Text      string  `json:"text,omitempty"`
	Score     float64 `json:"score,omitempty"`
	Committed bool    `json:"committed,omitempty"`
	Verdict   string  `json:"verdict,omitempty"`
	Err       string  `json:"err,omitempty"`
}
