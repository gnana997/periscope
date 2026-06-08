package awsssm

import (
	"bytes"
	"sync"
	"sync/atomic"
	"time"
)

// capBuf is a write-capped, truncation-flagging transcript buffer. It
// records up to max bytes verbatim; once full it keeps counting total
// bytes seen and sets truncated. Safe for concurrent writes (the
// os/exec stdout copier runs on its own goroutine).
type capBuf struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int64
	written   int64 // total bytes seen, pre-cap (the audit count)
	truncated bool
}

func (c *capBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written += int64(len(p))
	if room := c.max - int64(c.buf.Len()); room > 0 {
		if int64(len(p)) > room {
			c.buf.Write(p[:room])
			c.truncated = true
		} else {
			c.buf.Write(p)
		}
	} else if c.max >= 0 {
		c.truncated = true
	}
	return len(p), nil
}

// Bytes returns a copy of the captured (capped) transcript.
func (c *capBuf) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}

// activity tracks the last-byte timestamp as unix-nanos, lock-free, so
// the idle watchdog can tell whether the session is still live.
type activity struct{ ns atomic.Int64 }

func (a *activity) touch()          { a.ns.Store(time.Now().UnixNano()) }
func (a *activity) last() time.Time { return time.Unix(0, a.ns.Load()) }

// Write marks activity and reports the bytes as written (it discards
// them). Used as a tee target on the session's stdout.
func (a *activity) Write(p []byte) (int, error) { a.touch(); return len(p), nil }

// actWriter wraps a writer to mark activity on every successful write —
// used on the user→node (stdin) path so typing keeps the session alive.
type actWriter struct {
	w   interface{ Write([]byte) (int, error) }
	act *activity
}

func (aw actWriter) Write(p []byte) (int, error) {
	n, err := aw.w.Write(p)
	if n > 0 {
		aw.act.touch()
	}
	return n, err
}
