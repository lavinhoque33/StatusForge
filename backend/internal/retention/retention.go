// Package retention defines fixed storage lifetimes. TTL values are Unix seconds.
package retention

import "time"

const (
	EvidenceDays = 90
	WorkDays     = 7
	HistoryDays  = 365
)

func Evidence(at time.Time) int64 { return at.Add(EvidenceDays * 24 * time.Hour).Unix() }
func Work(at time.Time) int64     { return at.Add(WorkDays * 24 * time.Hour).Unix() }
func History(at time.Time) int64  { return at.Add(HistoryDays * 24 * time.Hour).Unix() }

// Expired treats missing expiry as permanent; observations and gaps require stricter checks.
func Expired(expiresAt int64, present bool, now time.Time) bool {
	return present && expiresAt <= now.Unix()
}
