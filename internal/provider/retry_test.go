package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	old := retrySleep
	retrySleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	t.Cleanup(func() { retrySleep = old })
	return &waits
}

const okSSE = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestStreamRetries429ThenSucceeds(t *testing.T) {
	waits := stubSleep(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
		case 2:
			w.WriteHeader(529)
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(okSSE))
		}
	}))
	defer srv.Close()
	c := &OpenAICompatClient{Endpoint: srv.URL}
	if _, err := c.Stream(context.Background(), Request{Model: "m"}, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if n.Load() != 3 || len(*waits) != 2 || (*waits)[0] != 2*time.Second {
		t.Fatalf("calls=%d waits=%v", n.Load(), *waits)
	}
}

func TestStreamGivesUpAfterThreeRetries(t *testing.T) {
	waits := stubSleep(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c := &OpenAICompatClient{Endpoint: srv.URL}
	if _, err := c.Stream(context.Background(), Request{Model: "m"}, nil); err == nil {
		t.Fatal("expected error")
	}
	if n.Load() != 4 || len(*waits) != 3 {
		t.Fatalf("calls=%d waits=%d", n.Load(), len(*waits))
	}
}

func TestRetryDelayJitterBounds(t *testing.T) {
	for a := 0; a < 3; a++ {
		d := retryDelay(http.Header{}, a, time.Now())
		base := retryBaseDelay << a
		if d < base/2 || d > base {
			t.Fatalf("attempt %d: %v", a, d)
		}
	}
}
