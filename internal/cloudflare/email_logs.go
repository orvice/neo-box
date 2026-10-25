package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Email Routing logs: the per-event rows of the GraphQL Analytics API
// dataset emailRoutingAdaptive, which is also what the dashboard's Email
// Routing activity log shows. The dataset is per Zone, kept for 31 days,
// and needs a token with the Analytics Read permission.
//
// Docs: https://developers.cloudflare.com/email-service/observability/metrics-analytics/

const (
	// EmailLogRetention is how far back Cloudflare keeps routing events.
	EmailLogRetention = 31 * 24 * time.Hour
	// EmailLogDefaultLimit and EmailLogMaxLimit bound the rows of one query.
	EmailLogDefaultLimit = 100
	EmailLogMaxLimit     = 500
)

// emailLogQuery lists the newest events of one Zone in a time range. The
// limit is part of the text (it is validated as an integer) so the query
// has no variable whose GraphQL type has to be guessed.
const emailLogQuery = `query EmailRoutingEvents($zoneTag: string!, $start: Time!, $end: Time!) {
  viewer {
    zones(filter: { zoneTag: $zoneTag }) {
      emailRoutingAdaptive(
        filter: { datetime_geq: $start, datetime_leq: $end }
        limit: %d
        orderBy: [datetime_DESC]
      ) {
        datetime
        from
        to
        subject
        status
        eventType
        action
        ruleMatched
        messageId
        sessionId
        errorDetail
        arc
        dkim
        dmarc
        spf
        isSpam
        isNDR
        isLastEvent
      }
    }
  }
}`

// flag reads a GraphQL uint8 (0 or 1) as a bool; a real boolean also works.
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	switch s := strings.TrimSpace(string(b)); s {
	case "true", "1":
		*f = true
	case "false", "0", "null":
		*f = false
	default:
		var n float64
		if err := json.Unmarshal(b, &n); err != nil {
			return fmt.Errorf("not a flag: %s", s)
		}
		*f = n != 0
	}
	return nil
}

// EmailLogEvent is one Email Routing event: an email arriving, being
// forwarded, dropped, rejected, or failing. One email can have several.
type EmailLogEvent struct {
	Time    time.Time `json:"datetime"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	// Status is Cloudflare's outcome, e.g. delivered, dropped, rejected.
	// The set of values is Cloudflare's; Neo Box passes it on.
	Status string `json:"status"`
	// EventType is incoming, forward, reply or newEmail.
	EventType string `json:"eventType"`
	// Action is what the matching rule did (forward, drop, worker).
	Action string `json:"action"`
	// RuleMatched is the ID of the routing rule that matched, if any.
	RuleMatched string `json:"ruleMatched"`
	MessageID   string `json:"messageId"`
	SessionID   string `json:"sessionId"`
	ErrorDetail string `json:"errorDetail"`
	// ARC, DKIM, DMARC and SPF are authentication results (e.g. pass).
	ARC         string `json:"arc"`
	DKIM        string `json:"dkim"`
	DMARC       string `json:"dmarc"`
	SPF         string `json:"spf"`
	IsSpam      flag   `json:"isSpam"`
	IsNDR       flag   `json:"isNDR"`
	IsLastEvent flag   `json:"isLastEvent"`
}

// EmailLogQuery selects a Zone's events: those with Start <= time <= End,
// newest first, at most Limit.
type EmailLogQuery struct {
	Start, End time.Time
	Limit      int
}

// Normalized applies the default limit.
func (q EmailLogQuery) Normalized() EmailLogQuery {
	if q.Limit == 0 {
		q.Limit = EmailLogDefaultLimit
	}
	return q
}

// Validate checks the range against what Cloudflare keeps and the limit
// against EmailLogMaxLimit. now is the current time.
func (q EmailLogQuery) Validate(now time.Time) error {
	switch {
	case q.Start.IsZero() || q.End.IsZero():
		return &FieldError{Field: "start_time", Message: "a start and an end time are required"}
	case !q.End.After(q.Start):
		return &FieldError{Field: "end_time", Message: "the end time must be after the start time"}
	case q.Start.Before(now.Add(-EmailLogRetention)):
		return &FieldError{Field: "start_time", Message: "Cloudflare keeps Email Routing logs for 31 days only"}
	case q.Limit < 1 || q.Limit > EmailLogMaxLimit:
		return &FieldError{Field: "limit", Message: fmt.Sprintf("the limit must be between 1 and %d", EmailLogMaxLimit)}
	}
	return nil
}

// ListEmailRoutingEvents returns a Zone's Email Routing events, newest
// first. It reads through the GraphQL Analytics API, which needs the
// Analytics Read permission on the Zone.
func (c *Client) ListEmailRoutingEvents(ctx context.Context, zoneID string, q EmailLogQuery) ([]EmailLogEvent, error) {
	q = q.Normalized()
	var data struct {
		Viewer struct {
			Zones []struct {
				Events []EmailLogEvent `json:"emailRoutingAdaptive"`
			} `json:"zones"`
		} `json:"viewer"`
	}
	vars := map[string]any{
		"zoneTag": zoneID,
		"start":   q.Start.UTC().Format(time.RFC3339),
		"end":     q.End.UTC().Format(time.RFC3339),
	}
	if err := c.graphql(ctx, fmt.Sprintf(emailLogQuery, q.Limit), vars, &data); err != nil {
		return nil, err
	}
	var out []EmailLogEvent
	for _, z := range data.Viewer.Zones {
		out = append(out, z.Events...)
	}
	return out, nil
}
