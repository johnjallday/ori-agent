package workspacecontinuity

import (
	"errors"
	"strings"
	"testing"
)

func TestRecordDTORequiresExplicitChoices(t *testing.T) {
	type choice struct {
		Enabled bool     `json:"enabled"`
		Names   []string `json:"names"`
		Note    string   `json:"note,omitempty"`
		Local   *bool    `json:"local,omitempty"`
	}
	for _, data := range []string{
		`{"names":[]}`, `{"enabled":false}`, `{"enabled":null,"names":[]}`,
		`{"enabled":false,"names":[],"note":null}`,
		`{"enabled":false,"Enabled":true,"names":[]}`,
		`{"enabled":false,"names":[],"api_key":"synthetic"}`,
	} {
		var value choice
		if err := DecodeRecord(Record{ID: "record", Data: []byte(data)}, &value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted incomplete/ambiguous DTO: %v", err)
		}
	}
	for _, data := range []string{`{"enabled":false,"names":[]}`, `{"enabled":true,"names":null,"local":null}`} {
		var value choice
		must(t, DecodeRecord(Record{ID: "record", Data: []byte(data)}, &value))
	}
	var value choice
	if err := DecodeRecord(Record{ID: "record", Data: []byte(strings.Repeat(" ", MaxRecordBytes+1))}, &value); !errors.Is(err, ErrLimit) {
		t.Fatalf("record size unchecked: %v", err)
	}
}
