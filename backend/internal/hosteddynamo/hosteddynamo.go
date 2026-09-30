// Package hosteddynamo builds the Lambda binaries' SDK clients from the SDK
// default configuration: execution-role credentials and AWS_REGION from the
// Lambda environment (ADR 0008 D4). It accepts no endpoint override. Only
// cmd/lambda-planner and cmd/lambda-worker import it; tests never construct
// these clients.
package hosteddynamo

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

var (
	errConfig = errors.New("AWS SDK default configuration could not be loaded")
	errRegion = errors.New("AWS_REGION: required")
	errSend   = errors.New("sqs SendMessageBatch failed")
)

// Load reads the SDK default configuration. Proxies from the environment are
// ignored; errors never carry configuration values.
func Load(ctx context.Context) (aws.Config, error) {
	client := awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
		t.Proxy = nil
	})
	cfg, err := config.LoadDefaultConfig(ctx, config.WithHTTPClient(client))
	if err != nil {
		return aws.Config{}, errConfig
	}
	if cfg.Region == "" {
		return aws.Config{}, errRegion
	}
	return cfg, nil
}

// DynamoDB is the regional DynamoDB client; any configured endpoint is dropped.
func DynamoDB(cfg aws.Config) *dynamodb.Client {
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = nil })
}

// SQS is the regional SQS client; any configured endpoint is dropped.
func SQS(cfg aws.Config) *sqs.Client {
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = nil })
}

// SendAPI is the SQS call the queue adapter makes.
type SendAPI interface {
	SendMessageBatch(
		context.Context,
		*sqs.SendMessageBatchInput,
		...func(*sqs.Options),
	) (*sqs.SendMessageBatchOutput, error)
}

// Queue adapts SendMessageBatch to the planner's Queue.
type Queue struct {
	API SendAPI
	URL string
}

// SendBatch sends up to ten bodies with entry IDs "0".."9" and returns the
// indexes SQS did not accept.
func (q Queue) SendBatch(ctx context.Context, bodies []string) ([]int, error) {
	entries := make([]sqstypes.SendMessageBatchRequestEntry, len(bodies))
	for i, body := range bodies {
		entries[i] = sqstypes.SendMessageBatchRequestEntry{
			Id:          aws.String(strconv.Itoa(i)),
			MessageBody: aws.String(body),
		}
	}
	out, err := q.API.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
		QueueUrl: aws.String(q.URL),
		Entries:  entries,
	})
	if err != nil {
		return nil, errSend
	}
	accepted := make(map[string]bool, len(out.Successful))
	for _, ok := range out.Successful {
		accepted[aws.ToString(ok.Id)] = true
	}
	var failed []int
	for i := range bodies {
		if !accepted[strconv.Itoa(i)] {
			failed = append(failed, i)
		}
	}
	return failed, nil
}
