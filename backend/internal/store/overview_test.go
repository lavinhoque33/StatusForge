package store

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestOverviewMembershipOrdering(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := dailyTestStore(t, now)
	app, _, err := s.CreateApplication(t.Context(), "Local service", now)
	if err != nil {
		t.Fatal(err)
	}
	create := func(name, outcome string, age time.Duration, open bool) monitor.Monitor {
		t.Helper()
		m := dailyMonitor(now.Add(-time.Hour))
		m.Name = name
		if open {
			m.ApplicationID = app.ID
		}
		if outcome != "" {
			done := monitor.Stamp(now.Add(-age))
			m.Evidence = &monitor.Evidence{
				ConfigVersion: m.ConfigVersion,
				Outcome:       outcome,
				StartedAt:     done,
				CompletedAt:   done,
			}
		}
		if open {
			m.OpenIncident = &incident.Open{
				ID:       "incident-" + name,
				OpenedAt: monitor.Stamp(now.Add(-time.Minute)),
			}
		}
		if e := s.Create(t.Context(), m); e != nil {
			t.Fatal(e)
		}
		return m
	}
	stale := create("A stale", "healthy", time.Minute, false)
	checker := create("B checker", "checker_problem", time.Second, false)
	unknown := create("C unknown", "", 0, false)
	failing := create("D failing", "failing", time.Second, false)
	opened := create("E open", "failing", time.Second, true)
	putIncident := func(m monitor.Monitor, state string, resolved *string) {
		t.Helper()
		in := Incident{
			ID:         "incident-" + m.Name,
			MonitorID:  m.ID,
			State:      state,
			OpenedAt:   monitor.Stamp(now.Add(-time.Minute)),
			ResolvedAt: resolved,
		}
		item, e := attributevalue.MarshalMap(in)
		if e != nil {
			t.Fatal(e)
		}
		item["PK"] = mustAV(incidentPK(m.ID))
		item["SK"] = mustAV("INC#" + in.ID)
		if _, e = s.db.PutItem(t.Context(), &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: item}); e != nil {
			t.Fatal(e)
		}
	}
	putIncident(opened, "open", nil)
	resolved := monitor.Stamp(now.Add(-10 * time.Second))
	putIncident(stale, "resolved", &resolved)
	r, e := s.Overview(t.Context(), now)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.OpenIncidents) != 1 || r.OpenIncidents[0].Monitor.ID != opened.ID ||
		len(r.FailingWithoutIncident) != 1 ||
		r.FailingWithoutIncident[0].Monitor.ID != failing.ID ||
		len(r.RecentRecoveries) != 1 ||
		r.RecentRecoveries[0].Monitor.ID != stale.ID {
		t.Fatalf("section membership: %+v", r)
	}
	if r.OpenIncidents[0].Monitor.ApplicationID == nil ||
		*r.OpenIncidents[0].Monitor.ApplicationID != app.ID ||
		r.OpenIncidents[0].Monitor.ApplicationName == nil ||
		*r.OpenIncidents[0].Monitor.ApplicationName != app.Name {
		t.Fatalf("application reference: %+v", r.OpenIncidents[0].Monitor)
	}
	if len(r.CoverageProblems) != 3 || r.CoverageProblems[0].Monitor.ID != stale.ID ||
		r.CoverageProblems[1].Monitor.ID != checker.ID ||
		r.CoverageProblems[2].Monitor.ID != unknown.ID {
		t.Fatalf("coverage ordering: %+v", r.CoverageProblems)
	}
	if r.Counts.Active != 5 || r.Counts.ByState["stale"] != 1 ||
		r.Counts.ByState["checker_problem"] != 1 {
		t.Fatalf("counts: %+v", r.Counts)
	}
}
