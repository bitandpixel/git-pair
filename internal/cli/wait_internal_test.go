package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"gitpair/internal/model"
)

// pollUntil carries `change wait`: it must stop promptly when the answer appears, stop
// on the deadline, and stop when the context goes away — without those, an agent either
// hangs or burns the repository with polls.

func TestPollUntilStopsWhenCheckSucceeds(t *testing.T) {
	calls := 0
	out, found, err := pollUntil(context.Background(), time.Millisecond, 0, func() (waitInput, bool, error) {
		calls++
		if calls < 3 {
			return waitInput{State: "READY"}, false, nil
		}
		return waitInput{State: "BLOCKED", Ref: "HEAD"}, true, nil
	})
	if err != nil {
		t.Fatalf("pollUntil: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if out.State != "BLOCKED" {
		t.Errorf("State = %q, want the value check reported when it found something", out.State)
	}
	if calls != 3 {
		t.Errorf("check ran %d times, want 3 (it must stop at the first yes)", calls)
	}
}

func TestPollUntilReportsTheLastObservationOnTimeout(t *testing.T) {
	calls := 0
	out, found, err := pollUntil(context.Background(), time.Millisecond, 50*time.Millisecond,
		func() (waitInput, bool, error) {
			calls++
			return waitInput{State: "READY", Fetches: calls}, false, nil
		})
	if err != nil {
		t.Fatalf("pollUntil: %v", err)
	}
	if found {
		t.Error("found = true after a timeout, want false")
	}
	if out.State != "READY" {
		t.Errorf("State = %q, want the last observation so the caller can report where things stand", out.State)
	}
	if out.Fetches != calls {
		t.Errorf("Fetches = %d, want the last check's %d", out.Fetches, calls)
	}
	if calls < 2 {
		t.Errorf("check ran %d times in 50ms at 1ms interval, want repeated polling", calls)
	}
}

func TestPollUntilStopsImmediatelyWhenAlreadyDone(t *testing.T) {
	// An agent that runs `change wait` after the review already landed must not sit
	// out the first interval.
	calls := 0
	started := time.Now()
	_, found, err := pollUntil(context.Background(), time.Hour, 0, func() (waitInput, bool, error) {
		calls++
		return waitInput{State: "APPROVED"}, true, nil
	})
	if err != nil || !found {
		t.Fatalf("pollUntil = (%v, %v), want an immediate result", found, err)
	}
	if calls != 1 {
		t.Errorf("check ran %d times, want 1", calls)
	}
	if time.Since(started) > time.Second {
		t.Errorf("waited %s despite an immediate answer", time.Since(started))
	}
}

func TestPollUntilStopsAtTheTimeoutNotTheInterval(t *testing.T) {
	// An agent that asks for 30 seconds of waiting must not be held by a polling
	// interval of an hour: the deadline, not the interval, has to wake it up.
	calls := 0
	started := time.Now()
	_, found, err := pollUntil(context.Background(), time.Hour, 80*time.Millisecond,
		func() (waitInput, bool, error) {
			calls++
			return waitInput{State: "READY"}, false, nil
		})
	if err != nil {
		t.Fatalf("pollUntil: %v", err)
	}
	if found {
		t.Error("found = true, want false")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("waited %s for an 80ms timeout at a 1h interval", elapsed)
	}
	if calls != 2 {
		t.Errorf("check ran %d times, want the first round and one at the deadline", calls)
	}
}

func TestPollUntilPropagatesCheckErrors(t *testing.T) {
	want := errors.New("repository went away")
	_, _, err := pollUntil(context.Background(), time.Millisecond, 0, func() (waitInput, bool, error) {
		return waitInput{}, false, want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestPollUntilStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		for calls == 0 {
			time.Sleep(time.Millisecond)
			calls++
			cancel()
		}
	}()
	_, _, err := pollUntil(ctx, 10*time.Millisecond, 0, func() (waitInput, bool, error) {
		return waitInput{State: "READY"}, false, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestActionableStates(t *testing.T) {
	// Everything the author has to act on, and nothing else: WORKING is the author's
	// own state and must not end a wait.
	for _, s := range []string{"BLOCKED", "FEEDBACK", "APPROVED"} {
		if !actionable(model.State(s)) {
			t.Errorf("%s is not actionable, want actionable", s)
		}
	}
	for _, s := range []string{"READY", "WORKING", ""} {
		if actionable(model.State(s)) {
			t.Errorf("%s is actionable, want not", s)
		}
	}
}
