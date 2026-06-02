package wire

// Event type discriminators carried in the "type" field of an Event. A single
// Event struct covers every line type; downstream switches on Type to know
// which fields are meaningful.
const (
	EventPartial   = "partial"   // in-progress transcript text
	EventFinal     = "final"     // committed transcript text
	EventRegex     = "regex"     // regex categorizer hit
	EventEmbedding = "embedding" // embedding categorizer hit
)

// Event is one JSON-line on the STT->LLM stream socket (/tmp/stt-system.sock).
// omitempty keeps each line to only the fields its Type uses. UttID is present
// on every event for a given utterance so the consumer and the orchestrator can
// correlate hits, partials, and the final back to one spoken turn.
type Event struct {
	UttID UttID `json:"utt_id,omitempty"` // correlation id (empty on a legacy/standalone producer)

	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	LatencyMs   int    `json:"latency_ms,omitempty"`
	StartedAtMs int64  `json:"started_at_ms,omitempty"` // unix-millis of first spoken word (transcript events)

	Category  string  `json:"category,omitempty"`
	Keyword   string  `json:"keyword,omitempty"`
	Score     float64 `json:"score,omitempty"`
	ElapsedMs int     `json:"elapsed_ms,omitempty"`
	Committed bool    `json:"committed,omitempty"`
}
