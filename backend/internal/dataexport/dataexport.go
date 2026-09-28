// Package dataexport copies local DynamoDB tables to validated private JSON Lines files.
package dataexport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type (
	Header struct {
		Format        string `json:"format"`
		ExportVersion int    `json:"exportVersion"`
		Table         string `json:"table"`
		DataFormat    *int   `json:"dataFormat"`
		AppVersion    string `json:"appVersion"`
		StartedAt     string `json:"startedAt"`
	}
	Trailer struct {
		Trailer    bool   `json:"trailer"`
		Items      int    `json:"items"`
		SHA256     string `json:"sha256"`
		FinishedAt string `json:"finishedAt"`
	}
	Result struct {
		Items    int
		Digest   string
		Duration time.Duration
		Warning  bool
	}
)

func jsonValue(v types.AttributeValue) (any, error) {
	switch t := v.(type) {
	case *types.AttributeValueMemberS:
		return map[string]any{"S": t.Value}, nil
	case *types.AttributeValueMemberN:
		return map[string]any{"N": t.Value}, nil
	case *types.AttributeValueMemberB:
		return map[string]any{"B": base64.StdEncoding.EncodeToString(t.Value)}, nil
	case *types.AttributeValueMemberBOOL:
		return map[string]any{"BOOL": t.Value}, nil
	case *types.AttributeValueMemberNULL:
		return map[string]any{"NULL": t.Value}, nil
	case *types.AttributeValueMemberSS:
		return map[string]any{"SS": t.Value}, nil
	case *types.AttributeValueMemberNS:
		return map[string]any{"NS": t.Value}, nil
	case *types.AttributeValueMemberBS:
		values := make([]string, len(t.Value))
		for i, b := range t.Value {
			values[i] = base64.StdEncoding.EncodeToString(b)
		}
		return map[string]any{"BS": values}, nil
	case *types.AttributeValueMemberM:
		m := make(map[string]any, len(t.Value))
		for k, v := range t.Value {
			x, e := jsonValue(v)
			if e != nil {
				return nil, e
			}
			m[k] = x
		}
		return map[string]any{"M": m}, nil
	case *types.AttributeValueMemberL:
		a := make([]any, len(t.Value))
		for i, v := range t.Value {
			x, e := jsonValue(v)
			if e != nil {
				return nil, e
			}
			a[i] = x
		}
		return map[string]any{"L": a}, nil
	default:
		return nil, errors.New("unsupported DynamoDB attribute")
	}
}

func encode(item map[string]types.AttributeValue) ([]byte, error) {
	m := make(map[string]any, len(item))
	for k, v := range item {
		x, e := jsonValue(v)
		if e != nil {
			return nil, e
		}
		m[k] = x
	}
	return json.Marshal(m)
}

func parseValue(raw json.RawMessage) (types.AttributeValue, error) {
	var one map[string]json.RawMessage
	if e := json.Unmarshal(raw, &one); e != nil || len(one) != 1 {
		return nil, errors.New("invalid DynamoDB attribute")
	}
	for kind, value := range one {
		switch kind {
		case "S":
			var v string
			if json.Unmarshal(value, &v) != nil {
				return nil, errors.New("invalid S")
			}
			return &types.AttributeValueMemberS{Value: v}, nil
		case "N":
			var v string
			if json.Unmarshal(value, &v) != nil {
				return nil, errors.New("invalid N")
			}
			if _, e := strconv.ParseFloat(v, 64); e != nil {
				return nil, e
			}
			return &types.AttributeValueMemberN{Value: v}, nil
		case "B":
			var v string
			if json.Unmarshal(value, &v) != nil {
				return nil, errors.New("invalid B")
			}
			b, e := base64.StdEncoding.DecodeString(v)
			return &types.AttributeValueMemberB{Value: b}, e
		case "BOOL":
			var v bool
			if json.Unmarshal(value, &v) != nil {
				return nil, errors.New("invalid BOOL")
			}
			return &types.AttributeValueMemberBOOL{Value: v}, nil
		case "NULL":
			var v bool
			if json.Unmarshal(value, &v) != nil || !v {
				return nil, errors.New("invalid NULL")
			}
			return &types.AttributeValueMemberNULL{Value: true}, nil
		case "SS":
			var v []string
			e := json.Unmarshal(value, &v)
			return &types.AttributeValueMemberSS{Value: v}, e
		case "NS":
			var v []string
			e := json.Unmarshal(value, &v)
			return &types.AttributeValueMemberNS{Value: v}, e
		case "BS":
			var v []string
			if e := json.Unmarshal(value, &v); e != nil {
				return nil, e
			}
			b := make([][]byte, len(v))
			for i, s := range v {
				var e error
				b[i], e = base64.StdEncoding.DecodeString(s)
				if e != nil {
					return nil, e
				}
			}
			return &types.AttributeValueMemberBS{Value: b}, nil
		case "M":
			var v map[string]json.RawMessage
			if e := json.Unmarshal(value, &v); e != nil {
				return nil, e
			}
			m := make(map[string]types.AttributeValue, len(v))
			for k, raw := range v {
				x, e := parseValue(raw)
				if e != nil {
					return nil, e
				}
				m[k] = x
			}
			return &types.AttributeValueMemberM{Value: m}, nil
		case "L":
			var v []json.RawMessage
			if e := json.Unmarshal(value, &v); e != nil {
				return nil, e
			}
			a := make([]types.AttributeValue, len(v))
			for i, raw := range v {
				x, e := parseValue(raw)
				if e != nil {
					return nil, e
				}
				a[i] = x
			}
			return &types.AttributeValueMemberL{Value: a}, nil
		}
		return nil, fmt.Errorf("unknown DynamoDB type %s", kind)
	}
	return nil, errors.New("invalid attribute")
}

func decode(raw []byte) (map[string]types.AttributeValue, error) {
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil || len(fields) == 0 {
		return nil, errors.New("invalid item")
	}
	item := make(map[string]types.AttributeValue, len(fields))
	for k, v := range fields {
		x, e := parseValue(v)
		if e != nil {
			return nil, e
		}
		item[k] = x
	}
	if _, ok := item["PK"].(*types.AttributeValueMemberS); !ok {
		return nil, errors.New("missing PK")
	}
	if _, ok := item["SK"].(*types.AttributeValueMemberS); !ok {
		return nil, errors.New("missing SK")
	}
	return item, nil
}

func Export(
	ctx context.Context,
	db *dynamodb.Client,
	table, path, version string,
) (result Result, err error) {
	started := time.Now()
	if e := os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
		return result, e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if e != nil {
		return result, e
	}
	defer func() {
		if e != nil || err != nil {
			_ = os.Remove(path)
		}
	}()
	defer f.Close()
	out := bufio.NewWriter(f)
	header := Header{
		"statusforge-export",
		1,
		table,
		nil,
		version,
		started.UTC().Format(time.RFC3339Nano),
	}
	fmtRow := func(v any) error {
		line, e := json.Marshal(v)
		if e != nil {
			return e
		}
		_, e = out.Write(append(line, '\n'))
		return e
	}
	marker, e := db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName: aws.String(table),
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "SYSTEM"},
				"SK": &types.AttributeValueMemberS{Value: "FORMAT"},
			},
		},
	)
	if e != nil {
		return result, e
	}
	if n, ok := marker.Item["dataFormat"].(*types.AttributeValueMemberN); ok {
		value, parseErr := strconv.Atoi(n.Value)
		if parseErr != nil {
			return result, errors.New("invalid table data format")
		}
		header.DataFormat = &value
	}
	live, e := db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName: aws.String(table),
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "SYSTEM"},
				"SK": &types.AttributeValueMemberS{Value: "LIVENESS"},
			},
		},
	)
	if e != nil {
		return result, e
	}
	result.Warning = livenessRecent(live.Item, started)
	if e = fmtRow(header); e != nil {
		return result, e
	}
	h := sha256.New()
	var cursor map[string]types.AttributeValue
	for {
		scan, e := db.Scan(
			ctx,
			&dynamodb.ScanInput{TableName: aws.String(table), ExclusiveStartKey: cursor},
		)
		if e != nil {
			return result, e
		}
		for _, item := range scan.Items {
			line, e := encode(item)
			if e != nil {
				return result, e
			}
			if _, e = out.Write(append(line, '\n')); e != nil {
				return result, e
			}
			_, _ = h.Write(append(line, '\n'))
			result.Items++
		}
		if len(scan.LastEvaluatedKey) == 0 {
			break
		}
		cursor = scan.LastEvaluatedKey
	}
	result.Digest = hex.EncodeToString(h.Sum(nil))
	if e = fmtRow(Trailer{true, result.Items, result.Digest, time.Now().UTC().Format(time.RFC3339Nano)}); e != nil {
		return result, e
	}
	if e = out.Flush(); e != nil {
		return result, e
	}
	if e = f.Sync(); e != nil {
		return result, e
	}
	result.Duration = time.Since(started)
	return result, nil
}

func livenessRecent(item map[string]types.AttributeValue, exportedAt time.Time) bool {
	at, ok := item["aliveAt"].(*types.AttributeValueMemberS)
	if !ok {
		return false
	}
	alive, err := time.Parse(time.RFC3339Nano, at.Value)
	if err != nil {
		return false
	}
	elapsed := exportedAt.Sub(alive)
	return elapsed >= 0 && elapsed <= 30*time.Second
}

// Validate reads the complete input before an importer creates or writes a table.
func Validate(path string) (Header, []map[string]types.AttributeValue, string, error) {
	f, e := os.Open(path)
	if e != nil {
		return Header{}, nil, "", e
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	line, e := reader.ReadBytes('\n')
	if e != nil {
		return Header{}, nil, "", e
	}
	var header Header
	var headerFields map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(line), &headerFields) != nil ||
		headerFields["dataFormat"] == nil {
		return Header{}, nil, "", errors.New("invalid export header")
	}
	if json.Unmarshal(bytes.TrimSpace(line), &header) != nil ||
		header.Format != "statusforge-export" ||
		header.ExportVersion != 1 ||
		header.Table == "" ||
		header.StartedAt == "" {
		return header, nil, "", errors.New("invalid export header")
	}
	h := sha256.New()
	items := []map[string]types.AttributeValue{}
	for {
		line, e = reader.ReadBytes('\n')
		if e != nil {
			return header, nil, "", fmt.Errorf("truncated export: %w", e)
		}
		var probe map[string]json.RawMessage
		if json.Unmarshal(bytes.TrimSpace(line), &probe) != nil {
			return header, nil, "", errors.New("invalid item line")
		}
		if _, ok := probe["trailer"]; ok {
			var t Trailer
			if json.Unmarshal(bytes.TrimSpace(line), &t) != nil || !t.Trailer ||
				t.Items != len(items) ||
				t.SHA256 != hex.EncodeToString(h.Sum(nil)) ||
				t.FinishedAt == "" {
				return header, nil, "", errors.New("export trailer count or digest mismatch")
			}
			extra, e := io.ReadAll(reader)
			if e != nil || len(extra) != 0 {
				return header, nil, "", errors.New("extra data after export trailer")
			}
			return header, items, t.SHA256, nil
		}
		item, e := decode(bytes.TrimSuffix(line, []byte{'\n'}))
		if e != nil {
			return header, nil, "", e
		}
		items = append(items, item)
		_, _ = h.Write(line)
	}
}

func Import(
	ctx context.Context,
	db *dynamodb.Client,
	table, path string,
	initialize func(context.Context) error,
) (Result, error) {
	started := time.Now()
	_, items, digest, e := Validate(path)
	if e != nil {
		return Result{}, e
	}
	// A pre-existing table must be empty before Initialize, which creates the format marker on a fresh table.
	describe, e := db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if e == nil && describe.Table != nil {
		scan, e := db.Scan(
			ctx,
			&dynamodb.ScanInput{TableName: aws.String(table), Limit: aws.Int32(1)},
		)
		if e != nil {
			return Result{}, e
		}
		if len(scan.Items) > 0 {
			return Result{}, errors.New("target table is not empty")
		}
	} else if e != nil {
		var missing *types.ResourceNotFoundException
		if !errors.As(e, &missing) {
			return Result{}, e
		}
	}
	if e = initialize(ctx); e != nil {
		return Result{}, e
	}
	_, e = db.DeleteItem(
		ctx,
		&dynamodb.DeleteItemInput{
			TableName: aws.String(table),
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "SYSTEM"},
				"SK": &types.AttributeValueMemberS{Value: "FORMAT"},
			},
		},
	)
	if e != nil {
		return Result{}, e
	}
	for start := 0; start < len(items); start += 25 {
		end := min(start+25, len(items))
		pending := make([]types.WriteRequest, 0, end-start)
		for _, item := range items[start:end] {
			pending = append(pending, types.WriteRequest{PutRequest: &types.PutRequest{Item: item}})
		}
		for len(pending) > 0 {
			out, e := db.BatchWriteItem(
				ctx,
				&dynamodb.BatchWriteItemInput{
					RequestItems: map[string][]types.WriteRequest{table: pending},
				},
			)
			if e != nil {
				return Result{}, e
			}
			pending = out.UnprocessedItems[table]
			if len(pending) > 0 {
				select {
				case <-ctx.Done():
					return Result{}, ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
	}
	return Result{Items: len(items), Digest: digest, Duration: time.Since(started)}, nil
}
