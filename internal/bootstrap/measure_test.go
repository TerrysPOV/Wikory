package bootstrap

import (
	"context"
	"testing"
	"time"
)

func TestMeasureReportsDurationTokensAcceptanceAndAProjection(t *testing.T) {
	ctx := context.Background()
	r, _, now, _ := newTestRuns(t)
	run := mustRun(t, r, 2, 8)
	_ = r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	b, _ := r.Next(ctx, run.ID, 50)
	if len(b.Items) != 2 {
		t.Fatalf("%d", len(b.Items))
	}
	// A submission adds its claim counts inside Complete, so they are in before the sample settles.
	if err := r.AddCounts(ctx, run.ID, 3, 1); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(40 * time.Second)
	if err := r.Complete(ctx, b.Items[0].ID, 1, Usage{InputTokens: 3000, OutputTokens: 300}, nil); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(20 * time.Second)
	if err := r.Complete(ctx, b.Items[1].ID, 1, Usage{InputTokens: 1000, OutputTokens: 100}, nil); err != nil {
		t.Fatal(err)
	}
	m, err := r.Measure(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.SampleItems != 2 || m.SampleDone != 2 || m.DurationSecs != 60 || m.TokensIn != 4000 || m.TokensOut != 400 || m.AcceptanceRate != 0.75 {
		t.Fatalf("%+v", m)
	}
	// 4400 tokens over 2 items is 2200 per item; 6 items remain.
	if m.ProjectedTokens != 6*2200 {
		t.Fatalf("projection = %d, want %d", m.ProjectedTokens, 6*2200)
	}
}

func TestMeasureStaysTheSampleAfterTheFullRunStarts(t *testing.T) {
	ctx := context.Background()
	r, _, now, _ := newTestRuns(t)
	run := mustRun(t, r, 2, 8)
	_ = r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	b, _ := r.Next(ctx, run.ID, 50)
	if err := r.AddCounts(ctx, run.ID, 3, 1); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	for _, it := range b.Items {
		if err := r.Complete(ctx, it.ID, it.Epoch, Usage{InputTokens: 2000, OutputTokens: 200}, nil); err != nil {
			t.Fatal(err)
		}
	}
	before, err := r.Measure(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The person confirms the full run and the other six items are worked.
	if err := r.Transition(ctx, run.ID, StateRunning, StateAwaiting); err != nil {
		t.Fatal(err)
	}
	rest, _ := r.Next(ctx, run.ID, 50)
	if len(rest.Items) != 6 {
		t.Fatalf("%d items left for the full run", len(rest.Items))
	}
	for _, it := range rest.Items {
		if err := r.Complete(ctx, it.ID, it.Epoch, Usage{InputTokens: 9000, OutputTokens: 900}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddCounts(ctx, run.ID, 40, 40); err != nil {
		t.Fatal(err)
	}
	after, err := r.Measure(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before || after.TokensIn != 4000 || after.TokensOut != 400 || after.AcceptanceRate != 0.75 {
		t.Fatalf("the sample's figures must not move once the full run adds its own: before %+v after %+v", before, after)
	}
}

func TestMeasureOfARunWithoutASampleIsZero(t *testing.T) {
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 3)
	m, err := r.Measure(context.Background(), run.ID)
	if err != nil || m.SampleItems != 0 || m.ProjectedTokens != 0 {
		t.Fatalf("%+v %v", m, err)
	}
}

// While the sample is still running the counters are live: the figures freeze only when it settles.
func TestMeasureIsLiveWhileTheSampleRuns(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 2, 8)
	_ = r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	b, _ := r.Next(ctx, run.ID, 50)
	if err := r.AddCounts(ctx, run.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, b.Items[0].ID, b.Items[0].Epoch, Usage{InputTokens: 700, OutputTokens: 70}, nil); err != nil {
		t.Fatal(err)
	}
	m, err := r.Measure(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.SampleItems != 2 || m.SampleDone != 1 || m.TokensIn != 700 || m.TokensOut != 70 || m.AcceptanceRate != 0.5 || m.ProjectedTokens != 770*6 {
		t.Fatalf("the live figures of a running sample: %+v", m)
	}
}
