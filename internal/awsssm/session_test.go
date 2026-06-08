package awsssm

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type stubSSM struct {
	startOut   *ssm.StartSessionOutput
	startErr   error
	startInput *ssm.StartSessionInput
	termInput  *ssm.TerminateSessionInput
	descOut    *ssm.DescribeInstanceInformationOutput
	descErr    error
}

func (s *stubSSM) StartSession(_ context.Context, in *ssm.StartSessionInput, _ ...func(*ssm.Options)) (*ssm.StartSessionOutput, error) {
	s.startInput = in
	return s.startOut, s.startErr
}

func (s *stubSSM) TerminateSession(_ context.Context, in *ssm.TerminateSessionInput, _ ...func(*ssm.Options)) (*ssm.TerminateSessionOutput, error) {
	s.termInput = in
	return &ssm.TerminateSessionOutput{}, nil
}

func (s *stubSSM) DescribeInstanceInformation(_ context.Context, _ *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return s.descOut, s.descErr
}

func TestOpenAndTerminate(t *testing.T) {
	stub := &stubSSM{startOut: &ssm.StartSessionOutput{
		SessionId:  aws.String("sess-123"),
		StreamUrl:  aws.String("wss://example/data"),
		TokenValue: aws.String("tok"),
	}}
	cfg := Config{Region: "us-east-1", InstanceID: "i-0abc1234", Reason: "actor=alice"}

	sess, err := open(context.Background(), stub, cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if sess.ID() != "sess-123" {
		t.Errorf("ID() = %q", sess.ID())
	}
	if aws.ToString(stub.startInput.Target) != "i-0abc1234" {
		t.Errorf("target = %q", aws.ToString(stub.startInput.Target))
	}
	if aws.ToString(stub.startInput.Reason) != "actor=alice" {
		t.Errorf("reason not forwarded")
	}
	if err := sess.Terminate(context.Background()); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if aws.ToString(stub.termInput.SessionId) != "sess-123" {
		t.Errorf("terminate session id = %q", aws.ToString(stub.termInput.SessionId))
	}
}

func TestOpen_StartRejected(t *testing.T) {
	stub := &stubSSM{startErr: errors.New("AccessDenied on document")}
	if _, err := open(context.Background(), stub, Config{InstanceID: "i-0abc1234"}); err == nil {
		t.Fatal("expected error when StartSession is rejected")
	}
}

func TestOpen_IncompleteResponse(t *testing.T) {
	stub := &stubSSM{startOut: &ssm.StartSessionOutput{SessionId: aws.String("x")}} // no StreamUrl/Token
	if _, err := open(context.Background(), stub, Config{InstanceID: "i-0abc1234"}); err == nil {
		t.Fatal("expected error for incomplete StartSession response")
	}
}

func TestPreflight(t *testing.T) {
	online := &stubSSM{descOut: &ssm.DescribeInstanceInformationOutput{
		InstanceInformationList: []ssmtypes.InstanceInformation{
			{PingStatus: ssmtypes.PingStatusOnline, PlatformName: aws.String("Amazon Linux")},
		},
	}}
	res, err := preflight(context.Background(), online, "i-0abc1234")
	if err != nil || !res.AgentOnline || res.PlatformName != "Amazon Linux" {
		t.Fatalf("online: res=%+v err=%v", res, err)
	}

	offline := &stubSSM{descOut: &ssm.DescribeInstanceInformationOutput{
		InstanceInformationList: []ssmtypes.InstanceInformation{{PingStatus: ssmtypes.PingStatusConnectionLost}},
	}}
	if res, _ := preflight(context.Background(), offline, "i-0abc1234"); res.AgentOnline {
		t.Error("connection-lost agent should not be Online")
	}

	none := &stubSSM{descOut: &ssm.DescribeInstanceInformationOutput{}}
	if res, err := preflight(context.Background(), none, "i-0abc1234"); err != nil || res.AgentOnline {
		t.Errorf("unregistered instance: res=%+v err=%v (want not-online, no error)", res, err)
	}

	broken := &stubSSM{descErr: errors.New("throttled")}
	if _, err := preflight(context.Background(), broken, "i-0abc1234"); err == nil {
		t.Error("expected error to propagate")
	}
}
