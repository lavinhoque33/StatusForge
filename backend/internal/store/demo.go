package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
)

var ErrDemoRefused = errors.New("demo_refused")

// PrepareDemo refuses all names except the dedicated demo table. Reset deletes
// only that table; a populated table is otherwise never overwritten.
func PrepareDemo(ctx context.Context, client *localdynamo.Client, table string, reset bool) error {
	if table != "statusforge_demo" {
		return fmt.Errorf("%w: only statusforge_demo may be seeded", ErrDemoRefused)
	}
	db := client.DynamoDB()
	_, err := db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		var missing *types.ResourceNotFoundException
		if errors.As(err, &missing) {
			return nil
		}
		return err
	}
	if reset {
		if _, err = db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)}); err != nil {
			return err
		}
		return dynamodb.NewTableNotExistsWaiter(db).
			Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 30*time.Second)
	}
	out, err := db.Scan(
		ctx,
		&dynamodb.ScanInput{
			TableName:            aws.String(table),
			Limit:                aws.Int32(1),
			ProjectionExpression: aws.String("PK,SK"),
		},
	)
	if err != nil {
		return err
	}
	if len(out.Items) != 0 {
		return fmt.Errorf(
			"%w: statusforge_demo contains data; set DEMO_RESET=yes to replace it",
			ErrDemoRefused,
		)
	}
	return nil
}

func (s *Store) MarkDemo(ctx context.Context) error {
	if s.table != "statusforge_demo" {
		return fmt.Errorf("%w: demo marker requires statusforge_demo", ErrDemoRefused)
	}
	if err := s.ensure(ctx); err != nil {
		return err
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table), Key: key("SYSTEM", "FORMAT"),
		UpdateExpression:          aws.String("SET demo = :demo"),
		ConditionExpression:       aws.String("attribute_exists(PK)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":demo": mustAV(true)},
	})
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
