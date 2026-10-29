package events

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func TestIsSuperseded(t *testing.T) {
	tests := map[string]struct {
		headSHA string
		err     error
		want    bool
	}{
		"head still at the checked commit": {headSHA: "abc", want: false},
		"head moved on":                    {headSHA: "def", want: true},
		"empty head":                       {headSHA: "", want: false},
		"lookup fails":                     {err: errors.New("boom"), want: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := vcsmocks.NewMockClient(t)
			client.On("GetPullRequestHeadSHA", mock.Anything, mock.Anything).Return(tt.headSHA, tt.err)

			ce := &CheckEvent{
				logger:      zerolog.Nop(),
				pullRequest: vcs.PullRequest{SHA: "abc"},
				ctr:         container.Container{VcsClient: client},
			}
			assert.Equal(t, tt.want, ce.isSuperseded(context.Background()))
		})
	}
}
