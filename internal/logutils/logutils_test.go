package logutils

import (
	"context"
	"errors"
	"testing"
)

func TestLoggerChainingPreservesError(t *testing.T) {
	t.Parallel()
	want := errors.New("root cause")
	base := &Logger{level: LevelDebug}

	tests := map[string]*Logger{
		"field":   base.WithError(want).WithField("movie_id", 1),
		"fields":  base.WithError(want).WithFields(map[string]any{"movie_id": 1}),
		"context": base.WithError(want).WithContext(context.Background()),
		"mixed": base.WithError(want).WithField("movie_id", 1).
			WithFields(map[string]any{"state": "stalledDL"}).WithContext(context.Background()),
		"reverse": base.WithField("movie_id", 1).WithContext(context.Background()).WithError(want),
	}

	for name, logger := range tests {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(logger.err, want) {
				t.Fatalf("chained logger error = %v, want %v", logger.err, want)
			}
		})
	}
}
