// Package wire is the single source of truth for the cross-system socket
// schemas of the voice pipeline: the STT->LLM stream Event, the LLM->TTS
// Command, and the UttID correlation key that threads one utterance through
// all three stages. Every repo (STT, LLM, TTS, orchestrator) imports these
// types instead of redeclaring them, so the wire format cannot drift.
//
// It is stdlib-only by design so it stays cheap for every consumer to import.
package wire

import (
	"strconv"
	"sync/atomic"
	"time"
)

// UttID is the correlation ID for one utterance. It originates in STT at
// speech-start and flows downstream on every Event, Command, and MetricEvent,
// so the orchestrator can join all of an utterance's per-stage timings into a
// single end-to-end latency. The string form survives JSON round-trips and is
// human-readable in logs.
type UttID string

// uttSeq disambiguates two utterances minted in the same millisecond.
var uttSeq atomic.Uint64

// NewUttID mints a monotonic, process-unique id of the form
// "<unixMillis>-<seq>". The millis prefix makes ids sortable and debuggable;
// the seq suffix guarantees uniqueness within a millisecond.
func NewUttID() UttID {
	n := uttSeq.Add(1)
	return UttID(strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + strconv.FormatUint(n, 10))
}

// String returns the id as a plain string.
func (u UttID) String() string { return string(u) }
