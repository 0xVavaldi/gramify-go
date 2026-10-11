package humanize

import (
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	cases := map[float64]string{
		512:                    "512 B",
		1000:                   "1000 B",
		4 * 1024 * 1024:        "4.0 MiB",
		2 * 1024 * 1024 * 1024: "2.0 GiB",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCompactBytes(t *testing.T) {
	cases := map[float64]string{
		512:                    "512B",
		4 * 1024 * 1024:        "4.0M",
		2 * 1024 * 1024 * 1024: "2.0G",
	}
	for in, want := range cases {
		if got := CompactBytes(in); got != want {
			t.Errorf("CompactBytes(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCount(t *testing.T) {
	cases := map[float64]string{
		512:           "512",
		1500:          "1.5k",
		2_500_000:     "2.5M",
		3_000_000_000: "3.0G",
	}
	for in, want := range cases {
		if got := Count(in); got != want {
			t.Errorf("Count(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestDuration(t *testing.T) {
	if got := Duration(125 * time.Second); got != "02:05" {
		t.Errorf("Duration(125s) = %q, want 02:05", got)
	}
	if got := Duration(3725 * time.Second); got != "1:02:05" {
		t.Errorf("Duration(3725s) = %q, want 1:02:05", got)
	}
}
