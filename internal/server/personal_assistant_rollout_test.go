package server

import "testing"

func TestPersonalAssistantRolloutEnabledFailsClosed(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"false", false}, {"unexpected", false},
		{"1", true}, {"TRUE", true}, {" yes ", true}, {"on", true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv(personalAssistantRolloutEnv, test.value)
			if got := personalAssistantRolloutEnabled(); got != test.want {
				t.Fatalf("personalAssistantRolloutEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}
