// Package awsssm opens AWS Systems Manager (SSM) Session Manager shells
// onto EC2 nodes on behalf of an individual user.
//
// The security model is per-user impersonation: a session is opened with
// short-lived credentials minted from the user's OIDC id_token via
// sts:AssumeRoleWithWebIdentity, never Periscope's own pod identity. An
// IAM trust policy on the assumed role — not Periscope config — is the
// load-bearing access gate, and CloudTrail attributes the session to the
// human.
//
// The byte transport is not reimplemented: StartSession yields a stream
// URL + token, and the AWS-maintained session-manager-plugin
// (Apache-2.0) drives the message-gateway data channel. This package
// runs that plugin as a subprocess, shuttling bytes between it and the
// caller's io.Reader/io.Writer (in production: the browser WebSocket),
// capturing a capped transcript and enforcing an idle timeout.
//
// Spike that validated this end-to-end: hack/poc-ssm-data-channel.
package awsssm
