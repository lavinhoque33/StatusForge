package main

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

func TestDemoGuardAndMarker(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if err != nil {
		t.Skip("DynamoDB Local unreachable:", err)
	}
	conn.Close()
	client := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := store.PrepareDemo(ctx, client, "statusforge_scratch_guard", true); !errors.Is(
		err,
		store.ErrDemoRefused,
	) {
		t.Fatalf("non-demo reset permitted: %v", err)
	}
	name := "statusforge_scratch_demoflag_" + rand.Text()
	scratch := store.New(client.DynamoDB(), name, time.Second, time.Now)
	if err := scratch.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client.DynamoDB().
			DeleteTable(cleanup, &dynamodb.DeleteTableInput{TableName: aws.String(name)})
	})
	system, err := scratch.System(ctx)
	if err != nil || system.Demo {
		t.Fatalf("ordinary table demo=%t: %v", system.Demo, err)
	}
	if err := scratch.MarkDemo(ctx); !errors.Is(err, store.ErrDemoRefused) {
		t.Fatalf("non-demo marker accepted: %v", err)
	}
	_, err = client.DynamoDB().
		UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(name), Key: map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "SYSTEM"}, "SK": &types.AttributeValueMemberS{Value: "FORMAT"}}, UpdateExpression: aws.String("SET demo = :value"), ExpressionAttributeValues: map[string]types.AttributeValue{":value": &types.AttributeValueMemberBOOL{Value: true}}})
	if err != nil {
		t.Fatal(err)
	}
	system, err = scratch.System(ctx)
	if err != nil || !system.Demo {
		t.Fatalf("marked table demo=%t: %v", system.Demo, err)
	}
	m := monitor.New(
		"kept",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/healthy",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
		},
		time.Now(),
	)
	if err := scratch.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareDemo(ctx, client, name, true); !errors.Is(err, store.ErrDemoRefused) {
		t.Fatalf("refusal lost: %v", err)
	}
	if _, err := scratch.Get(ctx, m.ID); err != nil {
		t.Fatalf("non-demo record touched: %v", err)
	}
	// A pre-existing operator demo table is read-only to this test.
	_, err = client.DynamoDB().
		DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("statusforge_demo")})
	if err == nil {
		t.Log("existing demo table left unchanged; checking its refusal only")
		if err := store.PrepareDemo(ctx, client, "statusforge_demo", false); !errors.Is(
			err,
			store.ErrDemoRefused,
		) {
			t.Fatalf("populated demo not refused: %v", err)
		}
		return
	}
	var absent *types.ResourceNotFoundException
	if !errors.As(err, &absent) {
		t.Fatal(err)
	}
	demo := store.New(client.DynamoDB(), "statusforge_demo", time.Second, time.Now)
	if err := demo.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client.DynamoDB().
			DeleteTable(cleanup, &dynamodb.DeleteTableInput{TableName: aws.String("statusforge_demo")})
	})
	if err := store.PrepareDemo(ctx, client, "statusforge_demo", false); !errors.Is(
		err,
		store.ErrDemoRefused,
	) {
		t.Fatalf("nonempty demo not refused: %v", err)
	}
	if err := demo.MarkDemo(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := demo.System(ctx)
	if err != nil || !status.Demo {
		t.Fatalf("demo flag absent: %+v %v", status, err)
	}
}
