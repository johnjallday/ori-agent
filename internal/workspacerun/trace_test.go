package workspacerun

import "testing"

func TestTracePageAfterCapsAndReportsMore(t *testing.T) {
	events := []TraceEvent{
		{Sequence: 1, Kind: TraceMessage},
		{Sequence: 2, Kind: TraceMessage},
		{Sequence: 3, Kind: TraceMessage},
	}

	page := TracePageAfter(events, 0, 2)
	if len(page.Events) != 2 {
		t.Fatalf("len(page.Events) = %d, want 2", len(page.Events))
	}
	if page.NextSince != 2 {
		t.Fatalf("NextSince = %d, want 2", page.NextSince)
	}
	if !page.HasMore {
		t.Fatal("HasMore = false, want true")
	}

	page = TracePageAfter(events, page.NextSince, 2)
	if len(page.Events) != 1 || page.Events[0].Sequence != 3 {
		t.Fatalf("second page events = %+v, want sequence 3", page.Events)
	}
	if page.HasMore {
		t.Fatal("second page HasMore = true, want false")
	}
}

func TestTraceTailReturnsRecentCopy(t *testing.T) {
	events := []TraceEvent{
		{Sequence: 1, Data: map[string]interface{}{"k": "v1"}},
		{Sequence: 2, Data: map[string]interface{}{"k": "v2"}},
		{Sequence: 3, Data: map[string]interface{}{"k": "v3"}},
	}

	tail := TraceTail(events, 2)
	if len(tail) != 2 || tail[0].Sequence != 2 || tail[1].Sequence != 3 {
		t.Fatalf("tail = %+v, want sequences 2 and 3", tail)
	}
	tail[0].Data["k"] = "changed"
	if events[1].Data["k"] != "v2" {
		t.Fatal("TraceTail did not return defensive copy")
	}
}
