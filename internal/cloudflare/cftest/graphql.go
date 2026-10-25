package cftest

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PermAnalyticsRead is Analytics Read, which the GraphQL Analytics API needs.
const PermAnalyticsRead = "analytics:read"

// EmailEvent is one Email Routing event of a Zone, as the GraphQL dataset
// emailRoutingAdaptive reports it.
type EmailEvent struct {
	Time                                      time.Time
	From, To, Subject, Status, EventType      string
	Action, RuleMatched, MessageID, SessionID string
	ErrorDetail, ARC, DKIM, DMARC, SPF        string
	IsSpam, IsNDR, IsLastEvent                bool
}

// AddEmailEvent records an Email Routing event for a Zone.
func (s *Server) AddEmailEvent(zoneID string, e EmailEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emailEvents[zoneID] = append(s.emailEvents[zoneID], e)
}

var graphqlLimit = regexp.MustCompile(`limit:\s*(\d+)`)

func b2n(b bool) int {
	if b {
		return 1
	}
	return 0
}

func gqlFail(w http.ResponseWriter, code, msg string) {
	// Like Cloudflare, a query the token may not run is answered with 200.
	writeJSON(w, http.StatusOK, map[string]any{
		"data":   nil,
		"errors": []map[string]any{{"message": msg, "extensions": map[string]any{"code": code}}},
	})
}

// graphql answers POST /graphql for the one query Neo Box sends: Email
// Routing events of a Zone (variables zoneTag, start, end; `limit: N` in the
// text), newest first.
func (s *Server) graphql(w http.ResponseWriter, r *http.Request, tok *Token, body map[string]any) {
	if r.Method != http.MethodPost {
		notFound(w, r.URL.Path)
		return
	}
	query, _ := body["query"].(string)
	vars, _ := body["variables"].(map[string]any)
	if !strings.Contains(query, "emailRoutingAdaptive") {
		gqlFail(w, "bad_request", "unknown field in the query")
		return
	}
	zoneID, _ := vars["zoneTag"].(string)
	z := s.zone(zoneID)
	if z == nil || !tok.has(PermAnalyticsRead, z.Account) {
		gqlFail(w, "authz", "zone '"+zoneID+"' does not have access to the path")
		return
	}
	start, err1 := time.Parse(time.RFC3339, str(vars["start"]))
	end, err2 := time.Parse(time.RFC3339, str(vars["end"]))
	if err1 != nil || err2 != nil {
		gqlFail(w, "bad_request", "start and end must be Time values")
		return
	}
	limit := 10
	if m := graphqlLimit.FindStringSubmatch(query); m != nil {
		limit, _ = strconv.Atoi(m[1])
	}
	var events []EmailEvent
	for _, e := range s.emailEvents[zoneID] {
		if !e.Time.Before(start) && !e.Time.After(end) {
			events = append(events, e)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.After(events[j].Time) })
	if len(events) > limit {
		events = events[:limit]
	}
	rows := []map[string]any{}
	for _, e := range events {
		rows = append(rows, map[string]any{
			"datetime": e.Time.UTC().Format(time.RFC3339), "from": e.From, "to": e.To, "subject": e.Subject,
			"status": e.Status, "eventType": e.EventType, "action": e.Action, "ruleMatched": e.RuleMatched,
			"messageId": e.MessageID, "sessionId": e.SessionID, "errorDetail": e.ErrorDetail,
			"arc": e.ARC, "dkim": e.DKIM, "dmarc": e.DMARC, "spf": e.SPF,
			"isSpam": b2n(e.IsSpam), "isNDR": b2n(e.IsNDR), "isLastEvent": b2n(e.IsLastEvent),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":   map[string]any{"viewer": map[string]any{"zones": []any{map[string]any{"emailRoutingAdaptive": rows}}}},
		"errors": nil,
	})
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
