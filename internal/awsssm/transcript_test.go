package awsssm

import "testing"

func TestCapBuf(t *testing.T) {
	c := &capBuf{max: 10}

	_, _ = c.Write([]byte("hello")) // 5 <= 10
	if c.written != 5 || c.truncated || string(c.Bytes()) != "hello" {
		t.Fatalf("after 5: written=%d trunc=%v buf=%q", c.written, c.truncated, c.Bytes())
	}

	_, _ = c.Write([]byte("world!!!")) // 5+8=13: only 5 more fit, rest dropped
	if c.written != 13 {
		t.Errorf("written = %d, want 13 (counts pre-cap total)", c.written)
	}
	if !c.truncated {
		t.Errorf("truncated should be set once over cap")
	}
	if got := string(c.Bytes()); got != "helloworld" || len(got) != 10 {
		t.Errorf("buf = %q (len %d), want capped at 10 bytes", got, len(got))
	}

	_, _ = c.Write([]byte("more")) // already full
	if string(c.Bytes()) != "helloworld" || c.written != 17 {
		t.Errorf("post-full: buf=%q written=%d", c.Bytes(), c.written)
	}
}
