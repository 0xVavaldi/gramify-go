package membudget

import "testing"

func TestParseSize(t *testing.T) {
	ok := map[string]int64{
		"1024":  1024,
		"1K":    1 << 10,
		"512k":  512 << 10,
		"256M":  256 << 20,
		"2G":    2 << 30,
		"4096B": 4096,
		" 8M ":  8 << 20,
	}
	for in, want := range ok {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}

	bad := []string{"", "abc", "12x", "-5M", "0", "0G", "M"}
	for _, in := range bad {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) expected error", in)
		}
	}
}

func TestSplitClampsAndReservesHeadroom(t *testing.T) {
	b := Split(1) // below MinBudget
	if b.Max != MinBudget {
		t.Errorf("Max = %d, want clamp to %d", b.Max, MinBudget)
	}

	// The modelled peak (churnFactor*buffers + runtimeReserve) must stay within
	// Max across a range of ceilings, so peak RSS lands near --memory-max.
	for _, max := range []int64{MinBudget, 256 << 20, 1 << 30, 8 << 30} {
		b = Split(max)
		buffers := b.Read + b.Proc
		if buffers >= b.Max {
			t.Errorf("max=%d: buffers(%d) should be < Max(%d)", max, buffers, b.Max)
		}
		modeledPeak := int64(churnFactor*float64(buffers)) + runtimeReserve
		if modeledPeak > b.Max {
			t.Errorf("max=%d: modeled peak %d exceeds Max %d", max, modeledPeak, b.Max)
		}
		if d := b.Read - b.Proc; d < -1 || d > 1 {
			t.Errorf("max=%d: Read(%d) and Proc(%d) should be ~equal", max, b.Read, b.Proc)
		}
	}
}
