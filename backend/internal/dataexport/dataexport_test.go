package dataexport_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/dataexport"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

func local(t *testing.T) (*localdynamo.Client, string, string) {
	t.Helper()
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unreachable:", e)
	}
	conn.Close()
	client := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local")
	source := "statusforge_test_export_" + rand.Text()
	target := "statusforge_test_import_" + rand.Text()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, table := range []string{source, target} {
			_, _ = client.DynamoDB().
				DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)})
		}
	})
	return client, source, target
}

func TestUnmarkedExportHeaderPreservesUnknownFormat(t *testing.T) {
	client, source, _ := local(t)
	ctx := t.Context()
	if err := store.New(client.DynamoDB(), source, time.Second, time.Now).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DynamoDB().DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(source),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "SYSTEM"},
			"SK": &types.AttributeValueMemberS{Value: "FORMAT"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "unmarked.jsonl")
	if _, err := dataexport.Export(ctx, client.DynamoDB(), source, path, "test"); err != nil {
		t.Fatal(err)
	}
	header, _, _, err := dataexport.Validate(path)
	if err != nil || header.DataFormat != nil {
		t.Fatalf("unmarked data format: %v, %v", header.DataFormat, err)
	}
}

func TestExportImportValidationAndRoundTrip(t *testing.T) {
	client, source, target := local(t)
	ctx := t.Context()
	src := store.New(client.DynamoDB(), source, time.Second, time.Now)
	if e := src.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	nested := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "PRIVATE"},
		"SK": &types.AttributeValueMemberS{Value: "nested"},
		"payload": &types.AttributeValueMemberM{
			Value: map[string]types.AttributeValue{
				"binary": &types.AttributeValueMemberB{Value: []byte{0, 255}},
				"list": &types.AttributeValueMemberL{
					Value: []types.AttributeValue{
						&types.AttributeValueMemberN{Value: "1.230"},
						&types.AttributeValueMemberBOOL{Value: false},
					},
				},
			},
		},
	}
	if _, e := client.DynamoDB().PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(source), Item: nested}); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "private.jsonl")
	result, e := dataexport.Export(ctx, client.DynamoDB(), source, file, "test")
	if e != nil || result.Items != 2 {
		t.Fatalf("export: %+v %v", result, e)
	}
	info, e := os.Stat(file)
	if e != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions: %v %v", info, e)
	}
	if _, e = dataexport.Export(ctx, client.DynamoDB(), source, file, "test"); !errors.Is(
		e,
		os.ErrExist,
	) {
		t.Fatalf("existing export path: %v", e)
	}
	raw, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	tampered := filepath.Join(t.TempDir(), "tampered.jsonl")
	changed := bytes.Replace(raw, []byte("1.230"), []byte("9.230"), 1)
	if bytes.Equal(raw, changed) {
		t.Fatal("fixture missing number")
	}
	if e = os.WriteFile(tampered, changed, 0o600); e != nil {
		t.Fatal(e)
	}
	dest := store.New(client.DynamoDB(), target, time.Second, time.Now)
	if _, e = dataexport.Import(ctx, client.DynamoDB(), target, tampered, dest.Initialize); e == nil {
		t.Fatal("tampered input accepted")
	}
	if _, e = client.DynamoDB().DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(target)}); e == nil {
		t.Fatal("invalid import created target")
	}
	imported, e := dataexport.Import(ctx, client.DynamoDB(), target, file, dest.Initialize)
	if e != nil || imported.Items != 2 || imported.Digest != result.Digest {
		t.Fatalf("import: %+v %v", imported, e)
	}
	other := filepath.Join(t.TempDir(), "other.jsonl")
	if _, e = dataexport.Export(ctx, client.DynamoDB(), target, other, "test"); e != nil {
		t.Fatal(e)
	}
	restored, e := os.ReadFile(other)
	if e != nil {
		t.Fatal(e)
	}
	originalLines := strings.Split(string(raw), "\n")
	restoredLines := strings.Split(string(restored), "\n")
	if len(originalLines) != len(restoredLines) {
		t.Fatalf("line counts differ %d %d", len(originalLines), len(restoredLines))
	}
	for _, line := range originalLines[1 : len(originalLines)-2] {
		if !strings.Contains(string(restored), line) {
			t.Fatalf("missing item after import")
		}
	}
	if _, e = dataexport.Import(ctx, client.DynamoDB(), target, file, dest.Initialize); e == nil {
		t.Fatal("non-empty target accepted")
	}
}
