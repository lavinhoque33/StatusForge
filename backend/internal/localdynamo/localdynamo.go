package localdynamo

import (
	"context"
	"errors"
	"net/http"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// Client uses only the explicitly configured local endpoint and static credentials.
type Client struct {
	client *dynamodb.Client
	host   string
}

func New(endpoint, host, region, accessKeyID, secretAccessKey string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	cfg := aws.Config{
		Region:           region,
		Credentials:      credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		BaseEndpoint:     aws.String(endpoint),
		RetryMaxAttempts: 1,
		HTTPClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	return &Client{client: dynamodb.NewFromConfig(cfg), host: host}
}

func (c *Client) Name() string { return c.host }

func (c *Client) Check(ctx context.Context) error { return c.Ping(ctx) }

// safeError keeps the original cause available for errors.Is/As classification
// without allowing SDK request URLs or credentials into server logs.
type safeError struct{ cause error }

func (e safeError) Error() string {
	var errno syscall.Errno
	if errors.As(e.cause, &errno) {
		return "dynamodb ListTables: " + errno.Error()
	}
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "dynamodb ListTables: context deadline exceeded"
	}
	return "dynamodb ListTables failed"
}

func (e safeError) Unwrap() error { return e.cause }

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.client.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)})
	if err != nil {
		return safeError{cause: err}
	}
	return nil
}
