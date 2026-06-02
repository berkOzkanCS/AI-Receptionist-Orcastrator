package wire

import "time"

// Command type discriminators carried in the "type" field.
const (
	CmdSpeak     = "speak"
	CmdInterrupt = "interrupt"
)

// Spoken-line kinds (the "kind" label on a speak Command).
const (
	KindFiller = "filler"
	KindAnswer = "answer"
	KindLLM    = "llm"
)

// Command is one JSON-line on the LLM->TTS socket (/tmp/llm-system.sock). It
// carries UttID so the TTS stage's playback timing joins back to the utterance
// that produced it. Fields use omitempty; only Type is always present.
type Command struct {
	UttID UttID `json:"utt_id,omitempty"` // correlation id of the originating utterance

	Type      string `json:"type"`                 // "speak" | "interrupt"
	Kind      string `json:"kind,omitempty"`       // "filler" | "answer" | "llm"
	Category  string `json:"category,omitempty"`   // full path, e.g. "logistics.hours"
	Text      string `json:"text,omitempty"`       // the words to synthesize
	ElapsedMs int    `json:"elapsed_ms,omitempty"` // upstream's ms since the caller's first word

	// Arrival is stamped by the TTS reader the instant the line is decoded; it
	// is the TTS-side t0 for latency accounting and is never serialized.
	Arrival time.Time `json:"-"`
}
