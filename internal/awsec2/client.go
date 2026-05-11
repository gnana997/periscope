// Package awsec2 wraps the AWS EC2 SDK with the minimal surface the
// CVE module needs: look up an instance's tags so the owner resolver
// can classify it as managed-nodegroup / karpenter-nodeclaim /
// unmanaged.
//
// Kept separate from internal/awsinspector so the two SDK service
// dependencies stay independent — the EC2 client is also useful to
// future modules (e.g. cost surfacing) without dragging in inspector2.
package awsec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// InstanceMeta is the projected EC2 metadata the owner resolver needs.
type InstanceMeta struct {
	InstanceID string
	AMI        string
	Tags       map[string]string
}

// API is the subset of the EC2 client surface this package uses.
// Defined as an interface so tests can stub it without making real
// AWS calls.
type API interface {
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// Client wraps the EC2 SDK with batched DescribeInstances.
type Client struct{ api API }

// New constructs a Client backed by the live SDK.
func New(cfg aws.Config) *Client { return &Client{api: ec2.NewFromConfig(cfg)} }

// NewWithAPI is the test seam — production code calls New.
func NewWithAPI(api API) *Client { return &Client{api: api} }

// describeInstancesMax is EC2's hard cap on a single
// DescribeInstances InstanceIds request. The SDK paginator handles
// follow-up pages when more results match, but the input list itself
// is capped, so we batch the input independently.
const describeInstancesMax = 100

// DescribeInstances returns one InstanceMeta per requested ID. IDs
// missing from the EC2 response (terminated / wrong account / typo)
// are silently omitted; callers should treat their absence as
// "unmanaged" via the owner resolver's fallback branch.
func (c *Client) DescribeInstances(ctx context.Context, ids []string) ([]InstanceMeta, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]InstanceMeta, 0, len(ids))
	for i := 0; i < len(ids); i += describeInstancesMax {
		end := i + describeInstancesMax
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[i:end]
		paginator := ec2.NewDescribeInstancesPaginator(c.api, &ec2.DescribeInstancesInput{
			InstanceIds: chunk,
		})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return out, fmt.Errorf("ec2 describe instances: %w", err)
			}
			for _, r := range page.Reservations {
				for _, inst := range r.Instances {
					out = append(out, projectInstance(inst))
				}
			}
		}
	}
	return out, nil
}

func projectInstance(in ec2types.Instance) InstanceMeta {
	m := InstanceMeta{Tags: make(map[string]string, len(in.Tags))}
	if in.ImageId != nil {
		m.AMI = *in.ImageId
	}
	if in.InstanceId != nil {
		m.InstanceID = *in.InstanceId
	}
	for _, t := range in.Tags {
		if t.Key == nil || t.Value == nil {
			continue
		}
		m.Tags[*t.Key] = *t.Value
	}
	return m
}
