package backuprecovery

import "time"

// EvidenceHealth describes independent stored-evidence dimensions, never backup
// success, exhaustive coverage or verified recovery. Evaluation performs no IO.
type EvidenceHealth struct {
	Summary       string   `json:"summary"`
	Observation   string   `json:"observation"`
	Recency       string   `json:"recency"`
	LatestCheck   string   `json:"latest_check"`
	Disconnection string   `json:"disconnection"`
	Capacity      string   `json:"capacity"`
	Coverage      string   `json:"coverage"`
	Problems      []string `json:"problems"`
}

func evaluateHealth(status Status, state *localState, now time.Time) EvidenceHealth {
	health := EvidenceHealth{Summary: "not_configured", Observation: "never_observed", Recency: "unknown", LatestCheck: "never_checked", Disconnection: "not_observed", Capacity: "unknown", Coverage: "unknown", Problems: []string{}}
	add := func(code string) {
		for _, prior := range health.Problems {
			if prior == code {
				return
			}
		}
		if len(health.Problems) < 8 {
			health.Problems = append(health.Problems, code)
		}
	}
	if status.Connection == "not_configured" {
		if status.LastSuccessfulRead != nil {
			health.Observation = "historical"
		}
		return health
	}
	health.Summary = "observed"
	if status.Connection != "configured" {
		health.Summary = "review_required"
		add("connection_review_required")
	}
	if !status.RuntimeAvailable {
		health.Summary = "unavailable"
		add("reader_unavailable")
	}
	if status.RemovalPending {
		health.Summary = "unavailable"
		add("removal_pending")
	}
	policy := proposedMonitoringPolicy()
	freshness := 24 * time.Hour
	if state != nil {
		if !state.Monitoring.valid() {
			health.Summary = "unavailable"
			add("policy_unavailable")
			return health
		}
		policy = state.Monitoring.Policy
		if state.Monitoring.Intent != "manual_only" {
			freshness = time.Duration(policy.FreshnessSeconds) * time.Second
		}
	}
	clockSuspect := !localTime(now)
	if status.LastAttempt != nil {
		attempt := status.LastAttempt
		if attempt.Generation != status.Generation {
			health.LatestCheck = "historical"
		} else if attempt.Outcome == "clock_suspect" || attempt.FinishedAt.After(now.Add(5*time.Minute)) || attempt.StartedAt.After(attempt.FinishedAt) {
			health.LatestCheck = "clock_suspect"
			clockSuspect = true
		} else {
			switch attempt.Outcome {
			case "read_ok":
				health.LatestCheck = "ok"
			case "cancelled":
				health.LatestCheck = "cancelled"
			case "destination_absent":
				health.LatestCheck = "destination_absent"
			default:
				health.LatestCheck = "failed"
				add("metadata_check_failed")
			}
		}
	}
	if status.InProgress {
		health.LatestCheck = "checking"
	}
	if status.LastSuccessfulRead != nil {
		switch {
		case !status.EvidenceCurrent:
			health.Observation = "historical"
		case status.LastSuccessfulRead.After(now.Add(5 * time.Minute)):
			health.Observation = "clock_suspect"
			clockSuspect = true
		case now.Sub(*status.LastSuccessfulRead) > freshness:
			health.Observation = "stale"
			add("observation_stale")
		default:
			health.Observation = "current"
		}
	}
	if health.Observation == "never_observed" && status.Connection == "configured" {
		health.Summary = "unavailable"
		add("observation_unavailable")
	}
	if status.EvidenceCurrent {
		switch {
		case status.LastCompletedAt != nil && status.LastCompletedAt.After(now.Add(5*time.Minute)):
			health.Recency = "clock_suspect"
			clockSuspect = true
		case status.SnapshotCount == nil:
			health.Recency = "unknown"
		case *status.SnapshotCount == 0:
			health.Recency = "no_completed_snapshot"
			add("no_completed_snapshot")
		case status.LastCompletedAt == nil:
			health.Recency = "completion_unavailable"
			add("completion_unavailable")
		case now.Sub(*status.LastCompletedAt) > time.Duration(policy.ExpectedAgeSeconds)*time.Second:
			health.Recency = "overdue"
			add("backup_completion_overdue")
		default:
			health.Recency = "recent"
		}
		if status.Coverage != nil {
			health.Coverage = "provider_declared_historical"
		}
	}
	if status.Destination == "identity_mismatch" {
		add("destination_identity_mismatch")
	}
	if status.Destination == "absent" {
		health.Disconnection = "unknown_duration"
		if state != nil {
			var first *time.Time
			allAbsent := true
			for _, attempt := range state.Attempts {
				if attempt.Generation != state.Generation || attempt.Outcome != "destination_absent" {
					allAbsent = false
					break
				}
				if attempt.FinishedAt.After(now.Add(5 * time.Minute)) {
					clockSuspect = true
					break
				}
				at := attempt.FinishedAt
				first = &at
			}
			if first != nil {
				if now.Sub(*first) >= time.Duration(policy.DisconnectedGraceSeconds)*time.Second {
					health.Disconnection = "grace_exceeded"
					add("destination_disconnected_beyond_grace")
				} else if !allAbsent || len(state.Attempts) < maxAttemptHistory {
					health.Disconnection = "within_grace"
				}
			}
		}
	}
	if status.Capacity != nil && status.Capacity.TotalBytes > 0 && status.Capacity.AvailableBytes <= status.Capacity.TotalBytes {
		switch health.Observation {
		case "current":
			// Exact floor(total*percent/100), without overflowing uint64. A
			// known zero available stays low; unknown is never changed to zero.
			total := status.Capacity.TotalBytes
			percentValue := policy.LowSpacePercent
			if percentValue < 1 || percentValue > 50 {
				health.Capacity = "unknown"
				return health
			}
			percent := uint64(percentValue)
			threshold := total/100*percent + (total%100*percent)/100
			if status.Capacity.AvailableBytes <= threshold {
				health.Capacity = "low"
				add("destination_space_low")
			} else {
				health.Capacity = "not_low"
			}
		case "stale":
			health.Capacity = "stale"
		case "historical":
			health.Capacity = "historical"
		default:
			health.Capacity = "unknown"
		}
	}
	if status.Monitoring == "review_required" {
		add("monitoring_review_required")
	}
	if clockSuspect {
		health.Summary = "unavailable"
		health.Observation = "clock_suspect"
		health.Capacity = "unknown"
		add("clock_suspect")
	}
	if len(health.Problems) > 0 && health.Summary == "observed" {
		health.Summary = "attention"
	}
	return health
}
