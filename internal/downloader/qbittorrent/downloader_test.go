package qbittorrent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryControlCallRecoversAfterTransientFailures(t *testing.T) {
	t.Parallel()
	attempts := 0
	err := retryControlCallWithBackoff(context.Background(), "test", func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary")
		}
		return nil
	}, func(int) time.Duration { return 0 })
	if err != nil || attempts != 3 {
		t.Fatalf("err=%v attempts=%d, want nil and 3", err, attempts)
	}
}

func TestRetryControlCallStopsOnContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retryControlCallWithBackoff(ctx, "test", func() error {
		return errors.New("temporary")
	}, func(int) time.Duration { return time.Hour })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}

func TestQbittorrentTorrentReadyToFinalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ti   TorrentInfo
		want bool
	}{
		{
			name: "downloading_with_full_progress_still_false",
			ti:   TorrentInfo{State: "downloading", Progress: 1.0, Size: 1e9, AmountLeft: 0},
			want: false,
		},
		{
			name: "allocating_with_zero_left_still_false",
			ti:   TorrentInfo{State: "allocating", Progress: 1.0, Size: 1e9, AmountLeft: 0},
			want: false,
		},
		{
			name: "stalledUP_with_full_progress",
			ti:   TorrentInfo{State: "stalledUP", Progress: 1.0},
			want: true,
		},
		{
			name: "uploading_with_amount_left_zero",
			ti:   TorrentInfo{State: "uploading", Size: 100, AmountLeft: 0, Progress: 0.5},
			want: true,
		},
		{
			name: "metaDL_never_true",
			ti:   TorrentInfo{State: "metaDL", Progress: 1.0, Size: 1, AmountLeft: 0},
			want: false,
		},
		{
			name: "error_never_true",
			ti:   TorrentInfo{State: "error", Progress: 1.0, Size: 1, AmountLeft: 0},
			want: false,
		},
		{
			name: "missingFiles_never_true",
			ti:   TorrentInfo{State: "missingFiles", Progress: 1.0, Size: 1, AmountLeft: 0},
			want: false,
		},
		{
			name: "pausedDL_done_when_bytes_match",
			ti:   TorrentInfo{State: "pausedDL", Progress: 1.0, Size: 100, AmountLeft: 0},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := qbittorrentTorrentReadyToFinalize(&tc.ti); got != tc.want {
				t.Fatalf("qbittorrentTorrentReadyToFinalize(%+v) = %v, want %v", tc.ti, got, tc.want)
			}
		})
	}
}

func TestCountVideoPathsInTorrentFiles(t *testing.T) {
	t.Parallel()
	n := countVideoPathsInTorrentFiles([]TorrentFileInfo{
		{Name: "readme.txt"},
		{Name: "video/S01E01.mkv"},
		{Name: "subs/eng.srt"},
	})
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestEpisodesChanCapacity(t *testing.T) {
	t.Parallel()
	if got := episodesChanCapacity(0); got != 1 {
		t.Fatalf("episodesChanCapacity(0) = %d, want 1", got)
	}
	if got := episodesChanCapacity(1); got != 1 {
		t.Fatalf("episodesChanCapacity(1) = %d, want 1", got)
	}
	if got := episodesChanCapacity(2); got != 2 {
		t.Fatalf("episodesChanCapacity(2) = %d, want 2", got)
	}
	if got := episodesChanCapacity(10); got != 10 {
		t.Fatalf("episodesChanCapacity(10) = %d, want 10", got)
	}
}
