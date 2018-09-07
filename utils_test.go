package main

import (
	"testing"
	"time"
)

func TestBackoff_DurationInitial(t *testing.T) {
	boff := Backoff{
		Min:    500 * time.Millisecond,
		Max:    5 * time.Minute,
		Factor: 2,
		Jitter: false,
	}

	testDur := boff.Duration()
	staticDur := time.Duration(500 * time.Millisecond)

	if testDur != staticDur {
		t.Logf("Backoff duration should be %v, but was %v", testDur, staticDur)
	}
}

func TestBackoff_DurationJitter(t *testing.T) {
	boff := Backoff{
		Min:    500 * time.Millisecond,
		Max:    5 * time.Minute,
		Factor: 2,
		Jitter: true,
	}

	// trigger 3 backoffs
	boff.Duration()
	boff.Duration()
	boff.Duration()

	testDur := boff.Duration()
	staticDur := time.Duration(4 * time.Second)

	// Should now be 8 seconds
	if testDur == staticDur {
		t.Logf("Backoff jitter is not random, should not be %v, but was %v", staticDur, testDur)
		t.Fail()
	}
}

func TestBackoff_DurationMultiple(t *testing.T) {
	boff := Backoff{
		Min:    500 * time.Millisecond,
		Max:    5 * time.Minute,
		Factor: 2,
		Jitter: false,
	}

	// simulate 3 backoffs
	boff.Duration()
	boff.Duration()
	boff.Duration()

	testDur := boff.Duration()
	staticDur := time.Duration(4 * time.Second)

	// Should now be 8 seconds
	if testDur != staticDur {
		t.Logf("Backoff duration should be %v, but was %v", testDur, staticDur)
		t.Fail()
	}
}

func TestBackoff_DurationZeros(t *testing.T) {
	boff := Backoff{
		Min:    0 * time.Millisecond,
		Max:    0 * time.Minute,
		Factor: 0,
		Jitter: false,
	}

	// simulate 1 backoff
	boff.Duration()

	testDur := boff.Duration()
	staticDur := time.Duration(200 * time.Millisecond)

	// Should be using defaults in function and not zero
	if testDur != staticDur {
		t.Logf("Backoff duration should be %v, but was %v", testDur, staticDur)
		t.Fail()
	}
}

func TestBackoff_DurationMax(t *testing.T) {
	boff := Backoff{
		Min:    500 * time.Millisecond,
		Max:    5 * time.Minute,
		Factor: 2,
		Jitter: false,
	}

	// simulate 100 backoffs to hit the Max time
	for i := 0; i < 100; i++ {
		boff.Duration()
	}

	testDur := boff.Duration()
	staticDur := time.Duration(5 * time.Minute)

	// Should be using defaults in function and not zero
	if testDur != staticDur {
		t.Logf("Backoff duration should be %v, but was %v", testDur, staticDur)
		t.Fail()
	}
}

func TestBackoff_Attempts(t *testing.T) {
	boff := Backoff{
		Min:    500 * time.Millisecond,
		Max:    5 * time.Minute,
		Factor: 2,
		Jitter: false,
	}

	// simulate 1 backoff
	boff.Duration()

	// Attempt should be 1
	if boff.Attempts() != 1 {
		t.Logf("Backoff attempts should be %v, but was %v", 1, boff.attempts)
		t.Fail()
	}
}

func TestBackoff_Reset(t *testing.T) {
	boff := Backoff{
		Min:    500 * time.Millisecond,
		Max:    5 * time.Minute,
		Factor: 2,
		Jitter: false,
	}

	// simulate 3 backoffs
	boff.Duration()
	boff.Duration()
	boff.Duration()

	// Now reset
	boff.Reset()

	testDur := boff.Duration()
	staticDur := time.Duration(500 * time.Millisecond)

	// should be back to first backoff period
	if testDur != staticDur {
		t.Logf("Backoff duration should be %v, but was %v", testDur, staticDur)
	}
}
