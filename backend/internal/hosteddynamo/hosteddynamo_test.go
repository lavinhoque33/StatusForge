package hosteddynamo

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeSQS stands in for the hosted client; no client is ever constructed.
type fakeSQS struct {
	accept func(id string) bool
	err    error
	input  *sqs.SendMessageBatchInput
}

func (f *fakeSQS) SendMessageBatch(
	_ context.Context,
	in *sqs.SendMessageBatchInput,
	_ ...func(*sqs.Options),
) (*sqs.SendMessageBatchOutput, error) {
	f.input = in
	if f.err != nil {
		return nil, f.err
	}
	out := &sqs.SendMessageBatchOutput{}
	for _, e := range in.Entries {
		if f.accept(aws.ToString(e.Id)) {
			out.Successful = append(out.Successful, sqstypes.SendMessageBatchResultEntry{Id: e.Id})
		} else {
			out.Failed = append(out.Failed, sqstypes.BatchResultErrorEntry{Id: e.Id})
		}
	}
	return out, nil
}

func TestQueueReportsUnacceptedEntries(t *testing.T) {
	api := &fakeSQS{accept: func(id string) bool { return id != "1" && id != "3" }}
	q := Queue{API: api, URL: "https://sqs.example.invalid/queue"}
	failed, err := q.SendBatch(t.Context(), []string{"a", "b", "c", "d"})
	if err != nil || !slices.Equal(failed, []int{1, 3}) {
		t.Fatalf("failed %v, err %v", failed, err)
	}
	if aws.ToString(api.input.QueueUrl) != q.URL ||
		aws.ToString(api.input.Entries[2].MessageBody) != "c" {
		t.Fatalf("request: %+v", api.input)
	}

	api.err = errors.New("AccessDenied for arn:aws:sqs:secret")
	if _, err := q.SendBatch(t.Context(), []string{"a"}); err == nil ||
		err.Error() != errSend.Error() {
		t.Fatalf("whole-call failure: %v", err)
	}
}
