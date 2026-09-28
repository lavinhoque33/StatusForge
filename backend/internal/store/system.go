package store

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/buildinfo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

type (
	RetentionPolicy struct {
		Record         string `json:"record"`
		Label          string `json:"label"`
		Days           int    `json:"days"`
		StartsFrom     string `json:"startsFrom"`
		ProtectedWhile string `json:"protectedWhile"`
	}
	TTLStatus struct {
		Status    string `json:"status"`
		Attribute string `json:"attribute"`
	}
	HousekeepingStatus struct {
		IntervalSeconds      int     `json:"intervalSeconds"`
		LastRunAt            *string `json:"lastRunAt"`
		PendingRetentionJobs int     `json:"pendingRetentionJobs"`
		PendingDeletions     int     `json:"pendingDeletions"`
	}
	SystemLimits struct {
		Workers                 int      `json:"workers"`
		MinIntervalSeconds      int      `json:"minIntervalSeconds"`
		DeliveryWorkers         int      `json:"deliveryWorkers"`
		DeliveryRetrySchedule   []string `json:"deliveryRetrySchedule"`
		ReminderIntervalSeconds int      `json:"reminderIntervalSeconds"`
		LivenessIntervalSeconds int      `json:"livenessIntervalSeconds"`
		AllowedTargets          string   `json:"allowedTargets"`
		NotifyURL               string   `json:"notifyUrl"`
		HistoryScanBound        int      `json:"historyScanBound"`
		SummaryObservationLimit int      `json:"summaryObservationLimit"`
		SummaryGapLimit         int      `json:"summaryGapLimit"`
		SummaryWindows          []int    `json:"summaryWindows"`
	}
)

type SystemStatus struct {
	Version             string             `json:"version"`
	Table               string             `json:"table"`
	DataFormat          int                `json:"dataFormat"`
	DataFormatWrittenBy string             `json:"dataFormatWrittenBy"`
	UpgradedFrom        *string            `json:"upgradedFrom"`
	Backfill            Backfill           `json:"backfill"`
	TTL                 TTLStatus          `json:"ttl"`
	Retention           []RetentionPolicy  `json:"retention"`
	Housekeeping        HousekeepingStatus `json:"housekeeping"`
	Limits              SystemLimits       `json:"limits"`
	Demo                bool               `json:"demo"`
}

func (s *Store) ConfigureSystem(limits SystemLimits, interval int) {
	s.systemLimits = limits
	s.housekeepingInterval = interval
}

func (s *Store) SetHousekeepingRun(now time.Time) {
	s.housekeepingMu.Lock()
	at := monitor.Stamp(now)
	s.lastHousekeepingRun = &at
	s.housekeepingMu.Unlock()
}

func (s *Store) System(ctx context.Context) (SystemStatus, error) {
	if e := s.ensure(ctx); e != nil {
		return SystemStatus{}, e
	}
	f, e := s.format(ctx)
	if e != nil {
		return SystemStatus{}, e
	}
	ttl, e := s.db.DescribeTimeToLive(
		ctx,
		&dynamodb.DescribeTimeToLiveInput{TableName: aws.String(s.table)},
	)
	if e != nil {
		return SystemStatus{}, ErrUnavailable
	}
	retain, e := s.RetentionJobs(ctx)
	if e != nil {
		return SystemStatus{}, e
	}
	deletes, e := s.pendingDeletionJobs(ctx)
	if e != nil {
		return SystemStatus{}, e
	}
	status := "unknown"
	attr := ""
	if ttl.TimeToLiveDescription != nil {
		status = string(ttl.TimeToLiveDescription.TimeToLiveStatus)
		attr = aws.ToString(ttl.TimeToLiveDescription.AttributeName)
	}
	s.housekeepingMu.Lock()
	run := s.lastHousekeepingRun
	s.housekeepingMu.Unlock()
	return SystemStatus{
		Version:             buildinfo.Current(),
		Table:               s.table,
		DataFormat:          f.DataFormat,
		DataFormatWrittenBy: f.WrittenBy,
		UpgradedFrom:        f.UpgradedFrom,
		Backfill:            f.Backfill,
		TTL:                 TTLStatus{status, attr},
		Retention: []RetentionPolicy{
			{
				"observations",
				"Checks",
				90,
				"check start",
				"none",
			}, {"gaps", "Coverage gaps", 90, "recorded time", "none"}, {"work", "Scheduling work", 7, "scheduled time", "none"}, {"run guards", "Heartbeat run guards", 7, "receipt time", "none"}, {"lifecycle", "Lifecycle events", 365, "event time", "none"}, {"maintenance", "Maintenance windows", 365, "effective end", "active or future windows"}, {"deployments", "Deployment markers", 365, "reported time", "none"}, {"incidents", "Incidents and delivery history", 365, "resolution time", "open incidents and non-final notifications"},
		},
		Housekeeping: HousekeepingStatus{s.housekeepingInterval, run, retain, deletes},
		Limits:       s.systemLimits,
		Demo:         f.Demo,
	}, nil
}
