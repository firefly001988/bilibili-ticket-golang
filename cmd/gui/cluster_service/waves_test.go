package cluster_service

import (
	"testing"
	"time"
)

func TestFirstPendingTaskGroupWaveKeepsSaleStartAsReflowBase(t *testing.T) {
	saleStart := time.Date(2026, 7, 8, 18, 0, 0, 0, time.UTC)
	paymentTimeout := 10 * time.Minute
	waveDuration := 3 * time.Minute
	maxWaves := 3

	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{name: "before sale", now: saleStart.Add(-time.Minute), want: 1},
		{name: "inside punctual wave", now: saleStart.Add(2 * time.Minute), want: 1},
		{name: "after punctual wave before first reflow", now: saleStart.Add(4 * time.Minute), want: 2},
		{name: "inside first reflow wave", now: saleStart.Add(11 * time.Minute), want: 2},
		{name: "after first reflow before second reflow", now: saleStart.Add(14 * time.Minute), want: 3},
		{name: "inside second reflow wave", now: saleStart.Add(21 * time.Minute), want: 3},
		{name: "after all waves", now: saleStart.Add(24 * time.Minute), want: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstPendingTaskGroupWave(saleStart, tt.now, paymentTimeout, waveDuration, maxWaves)
			if got != tt.want {
				t.Fatalf("firstPendingTaskGroupWave() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTaskGroupWaveStartUsesSaleStartPaymentIntervals(t *testing.T) {
	saleStart := time.Date(2026, 7, 8, 18, 0, 0, 0, time.UTC)
	paymentTimeout := 10 * time.Minute

	if got := taskGroupWaveStart(saleStart, 1, paymentTimeout); !got.Equal(saleStart) {
		t.Fatalf("wave 1 starts at %s, want %s", got, saleStart)
	}
	if got, want := taskGroupWaveStart(saleStart, 2, paymentTimeout), saleStart.Add(10*time.Minute); !got.Equal(want) {
		t.Fatalf("wave 2 starts at %s, want %s", got, want)
	}
	if got, want := taskGroupWaveStart(saleStart, 3, paymentTimeout), saleStart.Add(20*time.Minute); !got.Equal(want) {
		t.Fatalf("wave 3 starts at %s, want %s", got, want)
	}
}
