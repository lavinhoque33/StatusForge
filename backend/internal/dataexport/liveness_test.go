package dataexport

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestLivenessExportWarningOnlyForRecentReceipt(t *testing.T) {
	exportedAt := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		receipt string
		want    bool
	}{
		{"within window", exportedAt.Add(-29 * time.Second).Format(time.RFC3339Nano), true},
		{"exact boundary", exportedAt.Add(-30 * time.Second).Format(time.RFC3339Nano), true},
		{"stale receipt", exportedAt.Add(-31 * time.Second).Format(time.RFC3339Nano), false},
		{"future receipt", exportedAt.Add(time.Second).Format(time.RFC3339Nano), false},
		{"invalid receipt", "not-a-time", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := map[string]types.AttributeValue{
				"aliveAt": &types.AttributeValueMemberS{Value: tc.receipt},
			}
			if got := livenessRecent(row, exportedAt); got != tc.want {
				t.Fatalf("liveness warning = %v, want %v", got, tc.want)
			}
		})
	}
}
