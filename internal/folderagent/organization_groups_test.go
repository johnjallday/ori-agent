package folderagent

import (
	"strings"
	"testing"
)

func TestGroupEvidenceRequiresConnectedGroundedMembers(t *testing.T) {
	s := []ReviewSource{{Ref: "estimate"}, {Ref: "cabinet"}, {Ref: "invoice"}, {Ref: "unrelated"}, {Ref: "reference", Example: true}}
	e := []reviewExcerpt{{Ref: "s1", Text: "Kitchen remodel Maple Street estimate EST-400"}, {Ref: "s2", Text: "Kitchen remodel Maple Street estimate EST-400 purchase order PO-4387"}, {Ref: "s3", Text: "Invoice for cabinet order PO-4387"}, {Ref: "s4", Text: "Invoice for HVAC work PO-1234"}, {Ref: "s5", Text: "Maple Street", ExampleFolder: "Maple Street"}}
	okReply := `{"groups":[{"title":"Kitchen remodel","members":["s1","s2","s3"],"links":[{"a":"s1","b":"s2","key":"EST-400"},{"a":"s2","b":"s3","key":"PO-4387"}]}]}`
	for _, raw := range []string{okReply, "```json\n" + okReply + "\n```"} {
		g, ok := groundedGroups(e, s, raw)
		if !ok || len(g) != 1 || len(g[0].Members) != 3 || g[0].Reasons["invoice"] == "" {
			t.Fatal("supported group rejected")
		}
	}
	for _, raw := range []string{"nonsense", `{}`, strings.ReplaceAll(okReply, "PO-4387", "PO-9999"), strings.ReplaceAll(okReply, "s3", "s4"), strings.ReplaceAll(okReply, "s3", "s6"), strings.ReplaceAll(okReply, "s3", "s5"), strings.ReplaceAll(okReply, "Kitchen remodel", "Roof repair"), strings.Replace(okReply, `"s1","s2","s3"`, `"s1","s2","s3","s4"`, 1)} {
		if _, ok := groundedGroups(e, s, raw); ok {
			t.Fatal("unsupported group accepted")
		}
	}
	if g, ok := groundedGroups(e, s, `{"groups":[]}`); !ok || len(g) > 0 {
		t.Fatal("empty evidence must not invent a group")
	}
}
