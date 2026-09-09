package projecttemplates

import (
	"strconv"
	"strings"
)

// MaxAgentRoleIDLength bounds a derived role id so it stays inside the staffing
// adapter's role-id limit. Names longer than this truncate; the de-duplication
// pass in AgentRoleIDs still guarantees uniqueness afterwards.
const MaxAgentRoleIDLength = 80

// fallbackAgentRoleID is used when a name slugs away to nothing (a roster entry
// whose name is entirely punctuation or non-ASCII). It is never returned twice
// for one roster — AgentRoleIDs suffixes duplicates.
const fallbackAgentRoleID = "role"

// AgentRoleID derives the stable role identity of a template agent from its
// name (PRD D4). Template agents are positional today, and an index rebinds the
// wrong agent the moment a template is edited; a name-derived slug survives
// reordering and insertion.
//
// It is deliberately a pure function of the name so the same roster entry
// produces the same role id in the wizard, in the create request, and on the
// workspace afterwards. Use AgentRoleIDs for a whole roster: it applies this
// function and then guarantees the results are distinct.
func AgentRoleID(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	lastHyphen := true // suppresses a leading hyphen
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > MaxAgentRoleIDLength {
		slug = strings.Trim(slug[:MaxAgentRoleIDLength], "-")
	}
	if slug == "" {
		return fallbackAgentRoleID
	}
	return slug
}

// AgentRoleIDs returns one role id per spec, in roster order.
//
// normalizeAgentSpecs already de-duplicates a roster by case-insensitive name,
// so distinct entries almost always slug to distinct ids. Two names can still
// collapse onto one slug ("Mix Engineer" and "mix-engineer" are different names
// with the same slug), and two roles sharing an id would bind to each other's
// agents — so a repeat is suffixed "-2", "-3", … The first occurrence keeps the
// bare slug, which keeps the common case stable.
func AgentRoleIDs(specs []AgentSpec) []string {
	ids := make([]string, len(specs))
	used := make(map[string]int, len(specs))
	for i, spec := range specs {
		base := AgentRoleID(spec.Name)
		id := base
		for {
			count := used[id]
			used[id] = count + 1
			if count == 0 {
				break
			}
			id = base + "-" + strconv.Itoa(count+1)
		}
		ids[i] = id
	}
	return ids
}
