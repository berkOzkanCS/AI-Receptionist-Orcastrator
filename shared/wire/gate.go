package wire

// Half-duplex speaking gate. TTS (the server) publishes a "speaking until"
// timestamp whenever it is producing audio; STT (the client) must NOT feed mic
// audio to its backend while now < UntilMs, so the bot never transcribes its
// own voice through the speaker. This is the fix for acoustic feedback loops on
// open speakers (no echo cancellation).
//
// Both sides are on the same host, so the unix-millis clock is shared. The path
// defaults to DefaultSpeakingSocket; the orchestrator sets EnvSpeakingSocket on
// both children so they agree. Absent socket => no gating (standalone STT).
type SpeakingState struct {
	UntilMs int64 `json:"speaking_until_ms"`
}

const (
	// DefaultSpeakingSocket is the unix socket TTS listens on and STT dials.
	DefaultSpeakingSocket = "/tmp/tts-speaking.sock"
	// EnvSpeakingSocket overrides the socket path for both sides.
	EnvSpeakingSocket = "TTS_SPEAKING_SOCK"
)
