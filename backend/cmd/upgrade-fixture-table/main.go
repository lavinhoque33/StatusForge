package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
)

func main() {
	if len(os.Args) != 3 || !strings.HasPrefix(os.Args[2], "statusforge_m6fix_m") {
		fmt.Fprintln(os.Stderr, "usage: upgrade-fixture-table [check|delete] statusforge_m6fix_mN")
		os.Exit(2)
	}
	db := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").
		DynamoDB()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := os.Args[2]
	switch os.Args[1] {
	case "check":
		_, err := db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)})
		if err == nil {
			fmt.Fprintln(os.Stderr, "fixture table already exists:", name)
			os.Exit(1)
		}
		var missing *types.ResourceNotFoundException
		if !errors.As(err, &missing) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "delete":
		_, err := db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(name)})
		var missing *types.ResourceNotFoundException
		if err != nil && !errors.As(err, &missing) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		os.Exit(2)
	}
}
