package notifier

// QueueNotifier receives in-progress download events (queued, started, first episode, not supported).
// Callers (API or Telegram) provide an implementation; the download manager does not know the source.
type QueueNotifier interface {
	OnQueued(movieID uint, title string, position, maxConcurrent int)
	OnStarted(movieID uint, title string)
	OnFirstEpisodeReady(movieID uint, title string)
	OnVideoNotSupported(movieID uint, title string)
	OnStalled(movieID uint, title string)
}

// Noop is a QueueNotifier that does nothing. Use for API-originated downloads (no in-progress messages)
// or in tests that only need queue structure, not notifications.
var Noop QueueNotifier = noopQueueNotifier{}

type noopQueueNotifier struct{}

func (noopQueueNotifier) OnQueued(uint, string, int, int)  {}
func (noopQueueNotifier) OnStarted(uint, string)           {}
func (noopQueueNotifier) OnFirstEpisodeReady(uint, string) {}
func (noopQueueNotifier) OnVideoNotSupported(uint, string) {}
func (noopQueueNotifier) OnStalled(uint, string)           {}
