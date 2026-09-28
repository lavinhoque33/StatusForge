package store

import (
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/retention"
)

func (s *Store) expired(
	item map[string]types.AttributeValue,
) bool {
	return expiredAt(item, s.now())
}

func expiredAt(item map[string]types.AttributeValue, now time.Time) bool {
	value, ok := item["expiresAt"].(*types.AttributeValueMemberN)
	if !ok {
		return false
	}
	at, e := strconv.ParseInt(value.Value, 10, 64)
	return e == nil && retention.Expired(at, true, now)
}
