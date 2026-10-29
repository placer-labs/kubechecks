package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func TestCheckTimeoutCancelsHungCheck(t *testing.T) {
	done := make(chan error, 1)
	qm := NewQueueManager(Config{CheckTimeout: 50 * time.Millisecond},
		func(ctx context.Context, _ vcs.PullRequest, _ container.Container, _ []checks.ProcessorEntry, _ AIReviewChecker) error {
			<-ctx.Done()
			done <- ctx.Err()
			return ctx.Err()
		})
	t.Cleanup(func() { _ = qm.Shutdown(context.Background()) })

	require.NoError(t, qm.Enqueue(context.Background(), EnqueueParams{
		PullRequest: vcs.PullRequest{CloneURL: "https://github.com/example/repo.git", CheckID: 1},
	}))

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("check was not cancelled by the timeout")
	}
}

func blockingCheck(started chan<- vcs.PullRequest, causes chan<- error) ProcessFunc {
	return func(ctx context.Context, pr vcs.PullRequest, _ container.Container, _ []checks.ProcessorEntry, _ AIReviewChecker) error {
		started <- pr
		<-ctx.Done()
		causes <- context.Cause(ctx)
		return ctx.Err()
	}
}

func prAt(sha string) EnqueueParams {
	return EnqueueParams{PullRequest: vcs.PullRequest{CloneURL: "https://github.com/example/repo.git", CheckID: 7, SHA: sha}}
}

func TestNewCommitCancelsRunningCheck(t *testing.T) {
	started := make(chan vcs.PullRequest, 3)
	causes := make(chan error, 3)
	qm := NewQueueManager(Config{}, blockingCheck(started, causes))
	t.Cleanup(func() { qm.cancelAll() })

	require.NoError(t, qm.Enqueue(context.Background(), prAt("one")))
	assert.Equal(t, "one", (<-started).SHA)

	require.NoError(t, qm.Enqueue(context.Background(), prAt("two")))
	require.NoError(t, qm.Enqueue(context.Background(), prAt("three")))
	assert.ErrorIs(t, <-causes, pkg.ErrCheckSuperseded)

	select {
	case pr := <-started:
		assert.Equal(t, "three", pr.SHA, "the queued check for the intermediate commit must be dropped")
	case <-time.After(5 * time.Second):
		t.Fatal("latest commit was never checked")
	}
}

func TestSameCommitDoesNotCancelRunningCheck(t *testing.T) {
	started := make(chan vcs.PullRequest, 2)
	causes := make(chan error, 2)
	qm := NewQueueManager(Config{}, blockingCheck(started, causes))
	t.Cleanup(func() { qm.cancelAll() })

	require.NoError(t, qm.Enqueue(context.Background(), prAt("one")))
	<-started
	require.NoError(t, qm.Enqueue(context.Background(), prAt("one")))

	select {
	case err := <-causes:
		t.Fatalf("running check was cancelled: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestShutdownCancelsInFlightChecks(t *testing.T) {
	started := make(chan vcs.PullRequest, 1)
	causes := make(chan error, 1)
	qm := NewQueueManager(Config{CancelGrace: time.Second}, blockingCheck(started, causes))

	require.NoError(t, qm.Enqueue(context.Background(), prAt("one")))
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, qm.Shutdown(ctx), context.DeadlineExceeded)
	assert.ErrorIs(t, <-causes, context.Canceled)
}
