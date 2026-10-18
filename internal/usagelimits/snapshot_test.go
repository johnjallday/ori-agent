package usagelimits

import "testing"

func TestLevelFor(t *testing.T) {
	for _, tt := range []struct {
		name    string
		percent float64
		reached bool
		want    Level
	}{
		{"zero", 0, false, LevelOK},
		{"below warning", 79.99, false, LevelOK},
		{"warning boundary", 80, false, LevelWarn},
		{"below exhausted", 99.99, false, LevelWarn},
		{"exhausted boundary", 100, false, LevelExhausted},
		{"over limit", 110, false, LevelExhausted},
		{"reached overrides percent", 16, true, LevelExhausted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := LevelFor(tt.percent, tt.reached); got != tt.want {
				t.Fatalf("LevelFor = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWindowFor(t *testing.T) {
	for _, tt := range []struct {
		minutes int
		id      string
		label   string
	}{
		{300, "five_hour", "5-hour"},
		{10080, "weekly", "Weekly"},
		{60, "custom_60", "1-hour"},
		{90, "custom_90", "1.5-hour"},
		{1440, "custom_1440", "1-day"},
		{2880, "custom_2880", "2-day"},
	} {
		t.Run(tt.id, func(t *testing.T) {
			got := WindowFor(tt.minutes)
			if got.ID != tt.id || got.Label != tt.label || got.WindowMinutes != tt.minutes {
				t.Fatalf("WindowFor = %+v", got)
			}
		})
	}
}

func TestProviderSummary(t *testing.T) {
	p := Provider{Windows: []Window{
		{WindowMinutes: 10080, UsedPercent: 90},
		{WindowMinutes: 300, UsedPercent: 15},
	}}
	p.summarize(false)
	if p.Level != LevelWarn || p.Windows[0].WindowMinutes != 300 || p.Windows[1].Level != LevelWarn {
		t.Fatalf("summary = %+v", p)
	}
	p.summarize(true)
	if p.Level != LevelExhausted {
		t.Fatal("reached flag must override windows")
	}
	p.Windows[0].UsedPercent = 105
	p.summarize(false)
	if p.Level != LevelExhausted {
		t.Fatal("later warning window must not override exhausted window")
	}
}
