package events

import "time"

// Health issue events, published by the health checks (internal/health) when
// a problem appears or goes away. Data is a HealthIssueEvent. Notifications
// and the UI follow them; a still-present issue is not published again unless
// its severity changes.
const (
	HealthIssueRaised   Type = "health.issue_raised"
	HealthIssueResolved Type = "health.issue_resolved"
)

// HealthSeverity tells how much a health issue needs the user.
type HealthSeverity string

// Severities, from least to most urgent.
const (
	// SeverityInfo: worth knowing, nothing is affected yet.
	SeverityInfo HealthSeverity = "info"
	// SeverityWarning: something works less well; Gonema copes for now.
	SeverityWarning HealthSeverity = "warning"
	// SeverityError: something does not work until the user acts.
	SeverityError HealthSeverity = "error"
)

// Rank orders severities: info 1, warning 2, error 3, unknown 0.
func (s HealthSeverity) Rank() int {
	switch s {
	case SeverityInfo:
		return 1
	case SeverityWarning:
		return 2
	case SeverityError:
		return 3
	}
	return 0
}

// HealthIssue is a problem found by a health check, in plain language.
type HealthIssue struct {
	// ID is stable while the problem lasts, e.g. "indexer.unreachable:3".
	ID       string         `json:"id"`
	Severity HealthSeverity `json:"severity" enum:"info,warning,error"`
	// Title is a short summary, e.g. "Cinémathèque is not answering".
	Title string `json:"title"`
	// Message says what happened and what Gonema is doing about it.
	Message string `json:"message"`
	// Fix lists concrete steps to solve it, in order.
	Fix []string `json:"fix"`
	// Link is the path of the relevant page of the app, e.g. "/settings/indexers/3".
	Link string `json:"link,omitempty"`
	// Since is when the problem started (or was first seen).
	Since time.Time `json:"since"`
	// Source is the ID of the check that found it, e.g. "indexers".
	Source string `json:"source"`
}

// HealthIssueEvent is the data of health.issue_raised and health.issue_resolved.
type HealthIssueEvent struct {
	Issue HealthIssue `json:"issue"`
}
