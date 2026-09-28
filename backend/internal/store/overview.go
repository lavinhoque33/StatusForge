package store

import (
	"context"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

type (
	MonitorRef struct {
		ID              string  `json:"id"`
		Name            string  `json:"name"`
		Kind            string  `json:"kind"`
		ApplicationID   *string `json:"applicationId"`
		ApplicationName *string `json:"applicationName"`
	}
	IncidentItem struct {
		Monitor  MonitorRef `json:"monitor"`
		Incident Incident   `json:"incident"`
	}
	StatusItem struct {
		Monitor MonitorRef     `json:"monitor"`
		Status  monitor.Status `json:"status"`
	}
	OverviewCounts struct {
		Active   int            `json:"active"`
		Paused   int            `json:"paused"`
		Archived int            `json:"archived"`
		ByState  map[string]int `json:"byState"`
	}
	OverviewLimits struct {
		OpenIncidents       int `json:"openIncidents"`
		RecentRecoveries    int `json:"recentRecoveries"`
		Notifications       int `json:"notifications"`
		RecoveryWindowHours int `json:"recoveryWindowHours"`
	}
	OverviewNotification struct {
		Notification
		ApplicationID   *string `json:"applicationId"`
		ApplicationName *string `json:"applicationName"`
	}
	AttentionItems struct {
		Count int                    `json:"count"`
		Items []OverviewNotification `json:"items"`
	}
	OverviewResult struct {
		EvaluatedAt            string             `json:"evaluatedAt"`
		ReceiveOutages         []heartbeat.Outage `json:"receiveOutages"`
		OpenIncidents          []IncidentItem     `json:"openIncidents"`
		FailingWithoutIncident []StatusItem       `json:"failingWithoutIncident"`
		CoverageProblems       []StatusItem       `json:"coverageProblems"`
		Notifications          AttentionItems     `json:"notifications"`
		RecentRecoveries       []IncidentItem     `json:"recentRecoveries"`
		Counts                 OverviewCounts     `json:"counts"`
		Limits                 OverviewLimits     `json:"limits"`
	}
)

func (s *Store) Overview(ctx context.Context, now time.Time) (OverviewResult, error) {
	r := OverviewResult{
		EvaluatedAt:            monitor.Stamp(now),
		ReceiveOutages:         []heartbeat.Outage{},
		OpenIncidents:          []IncidentItem{},
		FailingWithoutIncident: []StatusItem{},
		CoverageProblems:       []StatusItem{},
		RecentRecoveries:       []IncidentItem{},
		Notifications:          AttentionItems{Items: []OverviewNotification{}},
		Counts: OverviewCounts{
			ByState: map[string]int{
				"healthy":         0,
				"late":            0,
				"failing":         0,
				"checker_problem": 0,
				"stale":           0,
				"unknown":         0,
			},
		},
		Limits: OverviewLimits{
			OpenIncidents:       50,
			RecentRecoveries:    20,
			Notifications:       10,
			RecoveryWindowHours: 24,
		},
	}
	ms, e := s.List(ctx)
	if e != nil {
		return r, e
	}
	apps, e := s.Applications(ctx)
	if e != nil {
		return r, e
	}
	appNames := map[string]string{}
	for _, a := range apps {
		appNames[a.ID] = a.Name
	}
	refs := make(map[string]MonitorRef, len(ms))
	for _, m := range ms {
		ref := MonitorRef{ID: m.ID, Name: m.Name, Kind: monitorKind(m)}
		if m.ApplicationID != "" {
			id := m.ApplicationID
			ref.ApplicationID = &id
			if name, ok := appNames[id]; ok {
				ref.ApplicationName = &name
			}
		}
		refs[m.ID] = ref
		switch m.Lifecycle {
		case "paused":
			r.Counts.Paused++
		case "archived":
			r.Counts.Archived++
		default:
			r.Counts.Active++
		}
		if m.Lifecycle != "archived" && m.OpenIncident != nil {
			in, e := s.incident(ctx, m.ID, m.OpenIncident.ID)
			if e != nil {
				return r, e
			}
			if e := s.overviewNotificationSummary(ctx, &in); e != nil {
				return r, e
			}
			in.MonitoringPaused = m.Lifecycle == "paused"
			in.InMaintenance = maintenanceActive(m, now)
			r.OpenIncidents = append(r.OpenIncidents, IncidentItem{ref, in})
		}
		if m.Lifecycle == "active" {
			status := monitor.WithStatus(m, now).Status
			r.Counts.ByState[status.State]++
			if status.State == "failing" && m.OpenIncident == nil {
				r.FailingWithoutIncident = append(r.FailingWithoutIncident, StatusItem{ref, status})
			}
			switch status.State {
			case "stale", "unknown", "checker_problem", "late":
				r.CoverageProblems = append(r.CoverageProblems, StatusItem{ref, status})
			}
		}
		// Incident keys are ordered by opening, not resolution. Read the newest 50
		// per monitor and select recoveries inside the 24-hour window.
		items, e := s.query(ctx, incidentPK(m.ID), "INC#", 50, true)
		if e != nil {
			return r, e
		}
		for _, item := range items {
			var in Incident
			if attributevalue.UnmarshalMap(item, &in) != nil {
				return r, ErrUnavailable
			}
			if in.ResolvedAt != nil && mustTime(*in.ResolvedAt).After(now.Add(-24*time.Hour)) &&
				!mustTime(*in.ResolvedAt).After(now) {
				if e := s.overviewNotificationSummary(ctx, &in); e != nil {
					return r, e
				}
				r.RecentRecoveries = append(r.RecentRecoveries, IncidentItem{ref, in})
			}
		}
	}
	sort.Slice(r.OpenIncidents, func(i, j int) bool {
		return r.OpenIncidents[i].Incident.OpenedAt > r.OpenIncidents[j].Incident.OpenedAt
	})
	if len(r.OpenIncidents) > 50 {
		r.OpenIncidents = r.OpenIncidents[:50]
	}
	rank := map[string]int{"stale": 0, "checker_problem": 1, "unknown": 2, "late": 3}
	sort.Slice(r.CoverageProblems, func(i, j int) bool {
		a, b := r.CoverageProblems[i], r.CoverageProblems[j]
		if rank[a.Status.State] != rank[b.Status.State] {
			return rank[a.Status.State] < rank[b.Status.State]
		}
		return a.Monitor.Name < b.Monitor.Name
	})
	sort.Slice(r.RecentRecoveries, func(i, j int) bool {
		return *r.RecentRecoveries[i].Incident.ResolvedAt > *r.RecentRecoveries[j].Incident.ResolvedAt
	})
	if len(r.RecentRecoveries) > 20 {
		r.RecentRecoveries = r.RecentRecoveries[:20]
	}
	notes, e := s.Attention(ctx, 10000)
	if e != nil {
		return r, e
	}
	r.Notifications.Count = len(notes)
	if len(notes) > 10 {
		notes = notes[:10]
	}
	for _, note := range notes {
		ref := refs[note.MonitorID]
		r.Notifications.Items = append(r.Notifications.Items, OverviewNotification{
			Notification: note, ApplicationID: ref.ApplicationID, ApplicationName: ref.ApplicationName,
		})
	}
	live, e := s.Liveness(ctx)
	if e != nil {
		return r, e
	}
	for _, o := range live.Outages {
		if o.To == "" || mustTime(o.To).After(now.Add(-24*time.Hour)) {
			r.ReceiveOutages = append(r.ReceiveOutages, o)
		}
	}
	return r, nil
}

func (s *Store) overviewNotificationSummary(ctx context.Context, in *Incident) error {
	items, err := s.query(ctx, incidentPK(in.MonitorID), "INCX#"+in.ID+"#NOTE#", 200, false)
	if err != nil {
		return err
	}
	for _, item := range items {
		var notification Notification
		if attributevalue.UnmarshalMap(item, &notification) != nil {
			return ErrUnavailable
		}
		in.NotificationSummary.count(notification)
	}
	return nil
}
