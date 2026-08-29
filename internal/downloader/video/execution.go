package ytdlp

import "sync"

// ytdlpExecutionMu prevents pip/self updates from modifying yt-dlp while a
// metadata probe or download process is using it.
var ytdlpExecutionMu sync.RWMutex

func acquireYTDLPExecution() func() {
	ytdlpExecutionMu.RLock()
	return ytdlpExecutionMu.RUnlock
}

func tryAcquireYTDLPUpdate() (func(), bool) {
	if !ytdlpExecutionMu.TryLock() {
		return nil, false
	}
	return ytdlpExecutionMu.Unlock, true
}
