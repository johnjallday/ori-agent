package learningmissions

import (
	"context"
	"database/sql"
	"slices"
)

// These are local behavioral records, not mastery or an assessment of a person.
// Independent means the versioned Ori path offered no additional coaching; it
// cannot establish the absence of help elsewhere.
type CompetencyProgress struct {
	Competency   string   `json:"competency"`
	State        string   `json:"state"`
	Attempts     int      `json:"attempts"`
	Guided       int      `json:"guided_attempts"`
	Independent  int      `json:"independent_attempts"`
	Unknown      int      `json:"unknown_support_attempts"`
	Reused       int      `json:"reused_attempts"`
	EvidenceKeys []string `json:"evidence_keys"`
}

type CompetencySummary struct {
	Coverage string               `json:"coverage"`
	Items    []CompetencyProgress `json:"items"`
}

func emptyCompetencies() CompetencySummary {
	out := CompetencySummary{Coverage: "available", Items: []CompetencyProgress{}}
	for _, name := range []string{"delegate", "evaluate", "improve", "reuse"} {
		out.Items = append(out.Items, CompetencyProgress{Competency: name, State: "not_yet_observed", EvidenceKeys: []string{}})
	}
	return out
}

func addCompetency(out *CompetencySummary, evidence Evidence, seen map[string]bool) {
	if !validDigest(evidence.Key) || evidence.CreatedAt <= 0 || evidence.SupportVersion < 0 ||
		!slices.Contains([]string{"delegate", "evaluate", "improve", "reuse"}, evidence.Competency) ||
		!slices.Contains([]Guidance{GuidanceBasic, GuidanceGuided, GuidanceUnknown}, evidence.Guidance) ||
		(evidence.Reused && evidence.Competency != "reuse") {
		out.Coverage = "partial"
		return
	}
	if seen[evidence.Key] {
		return
	}
	seen[evidence.Key] = true
	for n := range out.Items {
		item := &out.Items[n]
		if item.Competency != evidence.Competency {
			continue
		}
		item.Attempts++
		switch {
		case evidence.SupportVersion != GuidanceProtocolVersion || evidence.Guidance == GuidanceUnknown:
			item.Unknown++
		case evidence.Guidance == GuidanceBasic:
			item.Independent++
		case evidence.Guidance == GuidanceGuided:
			item.Guided++
		}
		if evidence.Reused {
			item.Reused++
		}
		switch {
		case item.Reused > 0:
			item.State = "reused_successfully"
		case item.Independent > 0:
			item.State = "practiced_independently"
		case item.Guided > 0:
			item.State = "practiced_with_guidance"
		default:
			item.State = "support_unknown"
		}
		if len(item.EvidenceKeys) < 3 {
			item.EvidenceKeys = append(item.EvidenceKeys, evidence.Key)
		}
	}
}

// Competencies reads minimal deduplicated metadata only. It does not load
// receipts/results or require source access to preserve already-earned facts.
func (s *SQLiteStore) Competencies(ctx context.Context, owner string) (CompetencySummary, error) {
	out := emptyCompetencies()
	if s == nil || s.db == nil || !validID(owner) {
		out.Coverage = "unknown"
		return out, ErrUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT dedup_key,json_extract(record,'$.competency'),json_extract(record,'$.guidance'),
 json_extract(record,'$.support_version'),json_extract(record,'$.reused'),created_at_ms FROM learning_evidence
 WHERE owner_id=? ORDER BY created_at_ms DESC,dedup_key LIMIT ?`, owner, MaxEvidence+1)
	if err != nil {
		out.Coverage = "unknown"
		return out, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	count := 0
	for rows.Next() {
		var e Evidence
		var support, reused sql.NullInt64
		if err := rows.Scan(&e.Key, &e.Competency, &e.Guidance, &support, &reused, &e.CreatedAt); err != nil {
			out.Coverage = "unknown"
			return out, ErrUnavailable
		}
		count++
		if count > MaxEvidence {
			out.Coverage = "partial"
			break
		}
		e.SupportVersion, e.Reused = int(support.Int64), reused.Int64 == 1
		if reused.Valid && reused.Int64 != 0 && reused.Int64 != 1 {
			out.Coverage = "partial"
			continue
		}
		addCompetency(&out, e, seen)
	}
	if rows.Err() != nil {
		out.Coverage = "unknown"
		return out, ErrUnavailable
	}
	if out.Coverage != "available" {
		for n := range out.Items {
			if out.Items[n].Attempts == 0 {
				out.Items[n].State = "coverage_unknown"
			}
		}
	}
	return out, nil
}
