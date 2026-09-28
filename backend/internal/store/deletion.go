package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

var (
	ErrDeleting     = errors.New("deleting")
	ErrNotArchived  = errors.New("not_archived")
	ErrNameMismatch = errors.New("confirm_name_mismatch")
)

func (s *Store) StartMonitorDeletion(
	ctx context.Context,
	id, name string,
	now time.Time,
) (monitor.Deletion, error) {
	m, e := s.Get(ctx, id)
	if e != nil {
		return monitor.Deletion{}, e
	}
	if m.Name != name {
		return monitor.Deletion{}, ErrNameMismatch
	}
	if m.Deletion != nil {
		return *m.Deletion, nil
	}
	if m.Lifecycle != "archived" {
		return monitor.Deletion{}, ErrNotArchived
	}
	at := monitor.Stamp(now)
	state := "deleting"
	if pending, e := s.monitorPending(ctx, id); e != nil {
		return monitor.Deletion{}, e
	} else if pending {
		state = "waiting_for_notifications"
	}
	status := monitor.Deletion{State: state, RequestedAt: at, UpdatedAt: at}
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName: aws.String(s.table),
				Key:       key("MONITORS", "MON#"+id),
				ConditionExpression: aws.String(
					"attribute_exists(PK) AND lifecycle = :archived AND attribute_not_exists(deletion) AND #name = :name",
				),
				UpdateExpression:         aws.String("SET deletion = :deletion"),
				ExpressionAttributeNames: map[string]string{"#name": "name"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":archived": mustAV("archived"),
					":name":     mustAV(name),
					":deletion": mustAV(status),
				},
			},
		},
		{
			Put: &types.Put{
				TableName: aws.String(s.table),
				Item:      key("JOBS", "JOB#delete-monitor#"+id),
			},
		},
	}
	_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if e != nil {
		if cancelled(e, 0) {
			return s.MonitorDeletion(ctx, id)
		}
		return status, ErrUnavailable
	}
	return status, nil
}

func (s *Store) MonitorDeletion(ctx context.Context, id string) (monitor.Deletion, error) {
	m, e := s.Get(ctx, id)
	if e != nil {
		return monitor.Deletion{}, e
	}
	if m.Deletion == nil {
		return monitor.Deletion{}, ErrNotFound
	}
	return *m.Deletion, nil
}

func (s *Store) StartApplicationDeletion(
	ctx context.Context,
	id, name string,
	now time.Time,
) (monitor.Deletion, error) {
	a, e := s.applicationRaw(ctx, id)
	if e != nil {
		return monitor.Deletion{}, e
	}
	if a.Name != name {
		return monitor.Deletion{}, ErrNameMismatch
	}
	if a.Deletion != nil {
		return *a.Deletion, nil
	}
	if a.ArchivedAt == nil {
		return monitor.Deletion{}, ErrNotArchived
	}
	at := monitor.Stamp(now)
	status := monitor.Deletion{State: "deleting", RequestedAt: at, UpdatedAt: at}
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName: aws.String(s.table),
				Key:       appKey(id),
				ConditionExpression: aws.String(
					"attribute_exists(archivedAt) AND attribute_not_exists(deletion) AND #name = :name",
				),
				UpdateExpression:         aws.String("SET deletion = :deletion"),
				ExpressionAttributeNames: map[string]string{"#name": "name"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":name":     mustAV(name),
					":deletion": mustAV(status),
				},
			},
		},
		{
			Put: &types.Put{
				TableName: aws.String(s.table),
				Item:      key("JOBS", "JOB#delete-application#"+id),
			},
		},
	}
	_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if e != nil {
		if cancelled(e, 0) {
			return s.ApplicationDeletion(ctx, id)
		}
		return status, ErrUnavailable
	}
	return status, nil
}

func (s *Store) ApplicationDeletion(ctx context.Context, id string) (monitor.Deletion, error) {
	a, e := s.applicationRaw(ctx, id)
	if e != nil {
		return monitor.Deletion{}, e
	}
	if a.Deletion == nil {
		return monitor.Deletion{}, ErrNotFound
	}
	return *a.Deletion, nil
}

func (s *Store) monitorPending(ctx context.Context, id string) (bool, error) {
	var cursor map[string]types.AttributeValue
	for {
		out, e := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk":     mustAV("MON#" + id),
					":prefix": mustAV("INCX#"),
				},
				Limit:             aws.Int32(100),
				ExclusiveStartKey: cursor,
				ConsistentRead:    aws.Bool(true),
			},
		)
		if e != nil {
			return false, ErrUnavailable
		}
		for _, it := range out.Items {
			if avString(it["entityType"]) == "notification" {
				switch avString(it["state"]) {
				case "pending", "retry_wait", "sending":
					return true, nil
				}
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return false, nil
		}
		cursor = out.LastEvaluatedKey
	}
}

func (s *Store) pendingDeletionJobs(ctx context.Context) (int, error) {
	ms, e := s.queryPage(ctx, "JOBS", "JOB#delete-monitor#", 10000)
	if e != nil {
		return 0, e
	}
	apps, e := s.queryPage(ctx, "JOBS", "JOB#delete-application#", 10000)
	return len(ms) + len(apps), e
}

func (s *Store) deletionProgress(ctx context.Context, entity, id, state string, count int) error {
	k := key("MONITORS", "MON#"+id)
	if entity == "application" {
		k = appKey(id)
	}
	_, e := s.db.UpdateItem(
		ctx,
		&dynamodb.UpdateItemInput{
			TableName:           aws.String(s.table),
			Key:                 k,
			ConditionExpression: aws.String("attribute_exists(deletion)"),
			UpdateExpression: aws.String(
				"SET deletion.#state = :state, deletion.updatedAt = :at, deletion.removedItems = deletion.removedItems + :count",
			),
			ExpressionAttributeNames: map[string]string{"#state": "state"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":state": mustAV(state),
				":at":    mustAV(monitor.Stamp(s.now())),
				":count": mustAV(count),
			},
		},
	)
	if e != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) deleteBatch(
	ctx context.Context,
	entity, id, pk, prefix string,
	filter func(string) bool,
) (bool, error) {
	var cursor map[string]types.AttributeValue
	keys := make([]map[string]types.AttributeValue, 0, 250)
	for {
		condition := "PK = :pk AND begins_with(SK, :prefix)"
		values := map[string]types.AttributeValue{":pk": mustAV(pk), ":prefix": mustAV(prefix)}
		if prefix == "" {
			condition = "PK = :pk"
			delete(values, ":prefix")
		}
		out, e := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:                 aws.String(s.table),
				KeyConditionExpression:    aws.String(condition),
				ExpressionAttributeValues: values,
				ConsistentRead:            aws.Bool(true),
				Limit:                     aws.Int32(300),
				ExclusiveStartKey:         cursor,
			},
		)
		if e != nil {
			return false, ErrUnavailable
		}
		for _, it := range out.Items {
			sk := avString(it["SK"])
			if filter != nil && !filter(sk) {
				continue
			}
			keys = append(keys, key(pk, sk))
			if len(keys) == 250 {
				break
			}
		}
		if len(keys) == 250 || len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	if len(keys) == 0 {
		return true, nil
	}
	type batchResult struct {
		written int
		err     error
	}
	results := make(chan batchResult, (len(keys)+24)/25)
	var batches sync.WaitGroup
	for first := 0; first < len(keys); first += 25 {
		end := min(first+25, len(keys))
		batches.Add(1)
		go func(rows []map[string]types.AttributeValue) {
			defer batches.Done()
			req := make([]types.WriteRequest, len(rows))
			for i, row := range rows {
				req[i] = types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: row}}
			}
			written := 0
			for len(req) > 0 {
				out, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
					RequestItems: map[string][]types.WriteRequest{s.table: req},
				})
				if err != nil {
					results <- batchResult{written, ErrUnavailable}
					return
				}
				next := out.UnprocessedItems[s.table]
				written += len(req) - len(next)
				req = next
				if len(req) > 0 {
					select {
					case <-ctx.Done():
						results <- batchResult{written, ctx.Err()}
						return
					case <-time.After(100 * time.Millisecond):
					}
				}
			}
			results <- batchResult{written, nil}
		}(keys[first:end])
	}
	batches.Wait()
	close(results)
	removed := 0
	var firstError error
	for result := range results {
		removed += result.written
		if firstError == nil && result.err != nil {
			firstError = result.err
		}
	}
	if removed > 0 {
		progressCtx := ctx
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			progressCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
		}
		if err := s.deletionProgress(progressCtx, entity, id, "deleting", removed); err != nil {
			return false, err
		}
	}
	if firstError != nil {
		return false, firstError
	}
	return false, nil
}

func (s *Store) finishDeletion(ctx context.Context, entity, id string) error {
	k := key("MONITORS", "MON#"+id)
	if entity == "application" {
		k = appKey(id)
	}
	tx := []types.TransactWriteItem{
		{
			Delete: &types.Delete{
				TableName:           aws.String(s.table),
				Key:                 k,
				ConditionExpression: aws.String("attribute_exists(deletion)"),
			},
		},
		{
			Delete: &types.Delete{
				TableName: aws.String(s.table),
				Key:       key("JOBS", "JOB#delete-"+entity+"#"+id),
			},
		},
	}
	_, e := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if e != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) processDeletions(ctx context.Context) (int, error) {
	total := 0
	var failures []error
	for _, entity := range []string{"monitor", "application"} {
		prefix := "JOB#delete-" + entity + "#"
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": mustAV("JOBS"), ":sk": mustAV(prefix),
			},
			Limit: aws.Int32(5), ConsistentRead: aws.Bool(true),
			ExclusiveStartKey: s.deletionJobCursors[entity],
		})
		if err != nil {
			failures = append(failures, ErrUnavailable)
			continue
		}
		s.deletionJobCursors[entity] = out.LastEvaluatedKey
		total += len(out.Items)
		for _, job := range out.Items {
			id := strings.TrimPrefix(avString(job["SK"]), prefix)
			if err := s.deleteOne(ctx, entity, id); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return total, errors.Join(failures...)
}

func (s *Store) deleteOne(ctx context.Context, entity, id string) error {
	if entity == "monitor" {
		pending, err := s.monitorPending(ctx, id)
		if err != nil {
			return err
		}
		if pending {
			return s.deletionProgress(ctx, entity, id, "waiting_for_notifications", 0)
		}
		for _, stage := range []struct {
			pk, prefix string
			filter     func(string) bool
		}{
			{"DELIVERY", "DUE#", func(sk string) bool {
				p := strings.SplitN(sk, "#", 5)
				return len(p) >= 4 && p[2] == id
			}},
			{"DELIVERY", "ATTENTION#" + id + "#", nil},
			{"MON#" + id, "", nil},
		} {
			complete, err := s.deleteBatch(ctx, entity, id, stage.pk, stage.prefix, stage.filter)
			if err != nil {
				return err
			}
			if !complete {
				return nil
			}
		}
	} else {
		complete, err := s.deleteBatch(ctx, entity, id, "APP#"+id, "", nil)
		if err != nil {
			return err
		}
		if !complete {
			return nil
		}
	}
	return s.finishDeletion(ctx, entity, id)
}
