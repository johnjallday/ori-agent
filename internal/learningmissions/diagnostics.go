package learningmissions

import (
	"context"
	"database/sql"
	"math"
	"time"
)

const MaxDiagnosticRows = 200
const DiagnosticWindow = 30 * 24 * time.Hour

// LocalDiagnostics contains bounded aggregate metadata only. It is not sent to
// the designer or an analytics service and makes no engagement/mastery claim.
type LocalDiagnostics struct {
	Since              int64             `json:"since_ms"`
	Until              int64             `json:"until_ms"`
	Coverage           string            `json:"coverage"`
	Offers             int               `json:"offers"`
	PersonalizedOffers int               `json:"personalized_offers"`
	AutomaticOffers    int               `json:"automatic_offers"`
	AcceptedOffers     int               `json:"accepted_offers"`
	DeferredOffers     int               `json:"deferred_offers"`
	DeclinedOffers     int               `json:"declined_offers"`
	StoppedContracts   int               `json:"stopped_contracts"`
	Decisions          map[string]int    `json:"decisions"`
	Practice           CompetencySummary `json:"practice"`
	DesignCalls        int               `json:"design_calls"`
	AutomaticCalls     int               `json:"automatic_calls"`
	KnownTokens        int64             `json:"known_tokens"`
	UnknownUsageCalls  int               `json:"unknown_usage_calls"`
	ReservedTokens     int64             `json:"reserved_tokens"`
	CallOutcomes       map[string]int    `json:"call_outcomes"`
}

func (s *Service) Diagnostics(ctx context.Context, owner string) (LocalDiagnostics, error) {
	if s == nil || s.store == nil || s.store.db == nil || !validID(owner) {
		return LocalDiagnostics{}, ErrUnavailable
	}
	now := s.now()
	out := LocalDiagnostics{Since: now.Add(-DiagnosticWindow).UnixMilli(), Until: now.UnixMilli(), Coverage: "available",
		Decisions: map[string]int{"use": 0, "revise": 0, "reject": 0}, CallOutcomes: map[string]int{"pending": 0, "succeeded": 0, "failed": 0, "unknown": 0, "invalidated": 0}}
	// These queries select scalars only, never definitions, prompts, notes,
	// correction/input text, Run payloads, artifacts or private source titles.
	read := func(query string, consume func(*sql.Rows) error) error {
		rows, err := s.store.db.QueryContext(ctx, query, owner, out.Since, out.Until, MaxDiagnosticRows+1)
		if err != nil {
			return ErrUnavailable
		}
		defer func() { _ = rows.Close() }()
		count := 0
		for rows.Next() {
			count++
			if count > MaxDiagnosticRows {
				out.Coverage = "partial"
				continue
			}
			if err := consume(rows); err != nil {
				out.Coverage = "partial"
			}
		}
		if rows.Err() != nil {
			return ErrUnavailable
		}
		return nil
	}
	if err := read(`SELECT state,COALESCE(json_extract(definition,'$.personalized'),0),COALESCE(json_extract(definition,'$.automatic'),0),COALESCE(json_extract(progress,'$.accepted_at_ms'),0)
 FROM learning_instances WHERE owner_id=? AND created_at_ms>=? AND created_at_ms<=? ORDER BY created_at_ms DESC,id LIMIT ?`, func(rows *sql.Rows) error {
		var state State
		var personalized, automatic bool
		var accepted int64
		if err := rows.Scan(&state, &personalized, &automatic, &accepted); err != nil || !state.Valid() || accepted < 0 {
			return ErrInvalid
		}
		out.Offers++
		if personalized {
			out.PersonalizedOffers++
		}
		if automatic {
			out.AutomaticOffers++
		}
		if accepted > 0 {
			out.AcceptedOffers++
		}
		switch state {
		case Deferred:
			out.DeferredOffers++
		case Declined:
			out.DeclinedOffers++
		case Stopped:
			out.StoppedContracts++
		}
		return nil
	}); err != nil {
		return LocalDiagnostics{}, err
	}
	if err := read(`SELECT json_extract(record,'$.decision') FROM learning_reviews WHERE owner_id=? AND json_extract(record,'$.decided_at_ms')>=? AND json_extract(record,'$.decided_at_ms')<=?
 ORDER BY json_extract(record,'$.decided_at_ms') DESC,id LIMIT ?`, func(rows *sql.Rows) error {
		var decision string
		if err := rows.Scan(&decision); err != nil {
			return ErrInvalid
		}
		if _, known := out.Decisions[decision]; !known {
			return ErrInvalid
		}
		out.Decisions[decision]++
		return nil
	}); err != nil {
		return LocalDiagnostics{}, err
	}
	if err := read(`SELECT status,COALESCE(json_extract(record,'$.automatic'),0),COALESCE(json_extract(record,'$.reserved_tokens'),0),json_extract(record,'$.actual_tokens')
 FROM learning_operations WHERE owner_id=? AND kind='generate' AND created_at_ms>=? AND created_at_ms<=? ORDER BY created_at_ms DESC,id LIMIT ?`, func(rows *sql.Rows) error {
		var status string
		var automatic bool
		var reserved int64
		var actual sql.NullInt64
		if err := rows.Scan(&status, &automatic, &reserved, &actual); err != nil {
			return ErrInvalid
		}
		if _, known := out.CallOutcomes[status]; !known || reserved < 0 || reserved > math.MaxInt64-out.ReservedTokens {
			return ErrInvalid
		}
		out.DesignCalls++
		out.CallOutcomes[status]++
		out.ReservedTokens += reserved
		if automatic {
			out.AutomaticCalls++
		}
		if !actual.Valid || actual.Int64 < 0 || actual.Int64 > math.MaxInt64-out.KnownTokens {
			out.UnknownUsageCalls++
			if actual.Valid {
				out.Coverage = "partial"
			}
		} else {
			out.KnownTokens += actual.Int64
		}
		return nil
	}); err != nil {
		return LocalDiagnostics{}, err
	}
	var err error
	out.Practice, err = s.store.Competencies(ctx, owner)
	for index := range out.Practice.Items {
		out.Practice.Items[index].EvidenceKeys = []string{}
	}
	if err != nil || out.Practice.Coverage != "available" {
		out.Coverage = "partial"
	}
	return out, nil
}
