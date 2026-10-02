package requestid

import (
	"strings"
	"testing"
	"time"
)

func TestNewIsSortableByTime(t *testing.T) {
	base := time.UnixMilli(1767225600000)
	earlier := newAt(base)
	later := newAt(base.Add(time.Millisecond))
	if len(earlier) != 26 || strings.Trim(earlier, alphabet) != "" {
		t.Fatalf("id = %q is not a 26 char Crockford base32 string", earlier)
	}
	if earlier[:10] >= later[:10] {
		t.Errorf("time prefix not increasing: %q >= %q", earlier, later)
	}
	if newAt(base) == earlier {
		t.Errorf("ids generated at the same instant collide")
	}
}

func TestTimestampPrefixMatchesSpecification(t *testing.T) {
	if got := newAt(time.UnixMilli(1469918176385))[:10]; got != "01ARYZ6S41" {
		t.Errorf("prefix = %q, want 01ARYZ6S41", got)
	}
}
