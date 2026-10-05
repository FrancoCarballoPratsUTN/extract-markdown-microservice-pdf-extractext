package usecase

import (
	"testing"
	"time"
)

// Each caller must receive the result of the job it submitted, never another
// caller's result.
func TestConcurrentSubmittersEachReceiveTheirOwnResult(t *testing.T) {
	wp := NewWorkerPool(&slowExtractor{}, 4, 8)
	defer wp.Close()

	first, err := wp.Submit([]byte("alpha"))
	if err != nil {
		t.Fatalf("submit alpha: %v", err)
	}
	second, err := wp.Submit([]byte("beta"))
	if err != nil {
		t.Fatalf("submit beta: %v", err)
	}

	for name, want := range map[string]struct {
		ch   chan JobResult
		text string
	}{
		"alpha": {first, "alpha"},
		"beta":  {second, "beta"},
	} {
		select {
		case res := <-want.ch:
			if res.Err != nil {
				t.Fatalf("%s: unexpected error %v", name, res.Err)
			}
			if res.Resp.Content != want.text {
				t.Errorf("%s: content = %q, want %q", name, res.Resp.Content, want.text)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: timed out waiting for result", name)
		}
	}
}