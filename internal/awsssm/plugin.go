package awsssm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Run drives session-manager-plugin for this session, streaming bytes
// between `in` (user → node, e.g. WebSocket reads) and `out` (node →
// user, e.g. WebSocket writes). It captures a capped transcript, enforces
// the idle timeout, and returns when the session ends — `in` reaches EOF,
// the plugin exits, the idle timeout fires, or ctx is cancelled.
//
// Unlike the laptop spike, this is pure pipe I/O with no controlling TTY:
// the browser's xterm.js is the terminal, so the server just shuttles
// bytes. That sidesteps the TTY/process-group complications interactive
// local use hit. Run does NOT terminate the SSM session — defer
// Session.Terminate for that, so cleanup happens on every exit path.
func (s *Session) Run(ctx context.Context, in io.Reader, out io.Writer) CloseResult {
	res := CloseResult{SessionID: s.ID()}
	start := time.Now()

	args, err := pluginArgs(s.cfg, s.out)
	if err != nil {
		res.Reason, res.Err, res.ExitCode = ReasonServerError, err, -1
		res.Duration = time.Since(start)
		return res
	}

	pluginPath := s.cfg.PluginPath
	if pluginPath == "" {
		pluginPath = defaultPlugin
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(runCtx, pluginPath, args...)
	cmd.Env = os.Environ()
	// Own process group so the idle watchdog / ctx cancel kills the
	// plugin AND any child it spawned. Safe here: no controlling TTY in
	// the pod, so no SIGTTIN/SIGTTOU contention (the trap interactive
	// local use fell into).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	transcript := &capBuf{max: s.cfg.TranscriptMax}
	act := &activity{}
	act.touch()

	// node → user: tee plugin stdout to the caller, the transcript, and
	// the activity tracker.
	cmd.Stdout = io.MultiWriter(out, transcript, act)
	var stderr capBuf
	stderr.max = 4096
	cmd.Stderr = &stderr

	// user → node: an *os.File pipe as stdin so os/exec hands the fd to
	// the child directly (no copier goroutine blocking Wait). We pump
	// `in` into the write end and close it on EOF so the plugin sees EOF.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		res.Reason, res.Err, res.ExitCode = ReasonServerError, err, -1
		res.Duration = time.Since(start)
		return res
	}
	cmd.Stdin = stdinR

	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		res.Reason, res.Err, res.ExitCode = ReasonServerError, fmt.Errorf("start %s: %w", pluginPath, err), -1
		res.Duration = time.Since(start)
		return res
	}
	_ = stdinR.Close() // child holds its own dup

	go func() {
		_, _ = io.Copy(actWriter{w: stdinW, act: act}, in)
		_ = stdinW.Close() // EOF to the plugin when the client hangs up
	}()

	var idled atomic.Bool
	if s.cfg.IdleTimeout > 0 {
		go watchIdle(runCtx, act, s.cfg.IdleTimeout, func() { idled.Store(true); cancel() })
	}

	waitErr := cmd.Wait()
	res.Duration = time.Since(start)
	res.Transcript = transcript.Bytes()
	res.TranscriptBytes = transcript.written
	res.Truncated = transcript.truncated
	res.ExitCode, res.Reason, res.Err = classify(waitErr, idled.Load(), ctx.Err(), stderr.Bytes())
	return res
}

// classify maps the plugin's exit into a CloseResult reason + code.
func classify(waitErr error, idled bool, parentErr error, stderr []byte) (int, string, error) {
	switch {
	case idled:
		return -1, ReasonIdleTimeout, nil
	case parentErr != nil:
		// The caller's context was cancelled (client hung up / shutdown).
		return -1, ReasonAbort, nil
	case waitErr == nil:
		return 0, ReasonCompleted, nil
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		// The remote shell / plugin exited non-zero. That's still a normal
		// session close, just with a non-zero code — not a server fault.
		return ee.ExitCode(), ReasonCompleted, nil
	}
	// Couldn't run/await the plugin at all.
	return -1, ReasonServerError, fmt.Errorf("plugin: %w (stderr: %s)", waitErr, string(stderr))
}

// watchIdle calls onIdle once no byte has flowed for `idle`. Activity =
// any stdin or stdout byte; there are no heartbeats on this channel.
func watchIdle(ctx context.Context, act *activity, idle time.Duration, onIdle func()) {
	t := time.NewTicker(idle / 4)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(act.last()) >= idle {
				onIdle()
				return
			}
		}
	}
}

// pluginArgs builds session-manager-plugin's argv, matching the AWS CLI's
// invocation: the plugin opens the message-gateway data channel from the
// StartSession response (arg 1) using the embedded token; the remaining
// args let it resume/terminate. The assumed-role credentials reach the
// plugin via the inherited process environment, so the profile arg is
// empty.
func pluginArgs(cfg Config, out *ssm.StartSessionOutput) ([]string, error) {
	sessionJSON, err := json.Marshal(map[string]string{
		"SessionId":  aws.ToString(out.SessionId),
		"StreamUrl":  aws.ToString(out.StreamUrl),
		"TokenValue": aws.ToString(out.TokenValue),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal session response: %w", err)
	}
	params := map[string]string{"Target": cfg.InstanceID}
	if cfg.DocumentName != "" {
		params["DocumentName"] = cfg.DocumentName
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal session params: %w", err)
	}
	endpoint := fmt.Sprintf("https://ssm.%s.amazonaws.com", cfg.Region)
	return []string{
		string(sessionJSON),
		cfg.Region,
		"StartSession",
		"", // profile — creds flow via the inherited environment
		string(paramsJSON),
		endpoint,
	}, nil
}
