package events

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func TestSettle(t *testing.T) {
	boom := errors.New("boom")
	cancelledBySupersede := func() context.Context {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(pkg.ErrCheckSuperseded)
		return ctx
	}
	tests := map[string]struct {
		ctx            context.Context
		err            error
		superseded     bool
		finalStatusSet bool
		wantStatus     *pkg.CommitState
		wantErr        error
	}{
		"early exit without error sets a terminal status": {err: nil, wantStatus: pkg.Pointer(pkg.StateSkip)},
		"error sets an error status":                       {err: boom, wantStatus: pkg.Pointer(pkg.StateError), wantErr: boom},
		"error status is set even after cancellation": {
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			err: context.Canceled, wantStatus: pkg.Pointer(pkg.StateError), wantErr: context.Canceled,
		},
		"status already set is left alone": {err: nil, finalStatusSet: true},
		"superseded before posting":        {err: nil, superseded: true},
		"cancelled by a newer commit":      {ctx: cancelledBySupersede(), err: context.Canceled},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := vcsmocks.NewMockClient(t)
			if tt.wantStatus != nil {
				client.On("CommitStatus", mock.MatchedBy(func(ctx context.Context) bool { return ctx.Err() == nil }), mock.Anything, *tt.wantStatus).Return(nil).Once()
			}
			ce := &CheckEvent{
				logger:         zerolog.Nop(),
				pullRequest:    vcs.PullRequest{SHA: "abc"},
				ctr:            container.Container{VcsClient: client},
				superseded:     tt.superseded,
				finalStatusSet: tt.finalStatusSet,
			}
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			err := ce.settle(ctx, tt.err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
