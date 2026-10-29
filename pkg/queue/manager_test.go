package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
