package github_client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	githubMocks "github.com/zapier/kubechecks/mocks/github_client/mocks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

const hookPullRequest = `{"number":9,"head":{"ref":"feature","sha":"abc","repo":{"name":"repo","full_name":"owner/repo","owner":{"login":"owner"}}},"base":{"ref":"main","repo":{"name":"repo","full_name":"owner/repo","owner":{"login":"owner"}}}}`

func parseHook(t *testing.T, c *Client, event, body string) (vcs.PullRequest, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/hooks", strings.NewReader(body))
	r.Header.Set("X-GitHub-Event", event)
	return c.ParseHook(context.Background(), r, []byte(body))
}

func TestParseHook_PullRequestActions(t *testing.T) {
	for name, tc := range map[string]struct {
		body    string
		handled bool
	}{
		"opened":            {`{"action":"opened","pull_request":` + hookPullRequest + `}`, true},
		"synchronize":       {`{"action":"synchronize","pull_request":` + hookPullRequest + `}`, true},
		"reopened":          {`{"action":"reopened","pull_request":` + hookPullRequest + `}`, true},
		"base changed":      {`{"action":"edited","changes":{"base":{"ref":{"from":"develop"}}},"pull_request":` + hookPullRequest + `}`, true},
		"title edited":      {`{"action":"edited","changes":{"title":{"from":"old"}},"pull_request":` + hookPullRequest + `}`, false},
		"body edited":       {`{"action":"edited","changes":{"body":{"from":"old"}},"pull_request":` + hookPullRequest + `}`, false},
		"edited no changes": {`{"action":"edited","pull_request":` + hookPullRequest + `}`, false},
		"closed":            {`{"action":"closed","pull_request":` + hookPullRequest + `}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := parseHook(t, &Client{}, "pull_request", tc.body)
			if !tc.handled {
				assert.ErrorIs(t, err, vcs.ErrInvalidType)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 9, pr.CheckID)
			assert.Equal(t, "abc", pr.SHA)
		})
	}
}

func TestParseHook_IssueComment(t *testing.T) {
	const pullRequestIssue = `{"number":9,"pull_request":{"url":"https://api.github.com/repos/owner/repo/pulls/9"}}`
	const plainIssue = `{"number":9}`
	repo := `"repository":{"name":"repo","owner":{"login":"owner"}}`

	for name, tc := range map[string]struct {
		body    string
		handled bool
	}{
		"replan on a PR":         {`{"action":"created","issue":` + pullRequestIssue + `,"comment":{"body":"kubechecks again"},` + repo + `}`, true},
		"replan with whitespace": {`{"action":"created","issue":` + pullRequestIssue + `,"comment":{"body":"  Kubechecks Again\r\n"},` + repo + `}`, true},
		"replan on an issue":     {`{"action":"created","issue":` + plainIssue + `,"comment":{"body":"kubechecks again"},` + repo + `}`, false},
		"other comment":          {`{"action":"created","issue":` + pullRequestIssue + `,"comment":{"body":"lgtm"},` + repo + `}`, false},
		"edited comment":         {`{"action":"edited","issue":` + pullRequestIssue + `,"comment":{"body":"kubechecks again"},` + repo + `}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			mockPR := githubMocks.NewMockPullRequestsServices(t)
			if tc.handled {
				mockPR.On("Get", mock.Anything, "owner", "repo", 9).Return(&github.PullRequest{
					Number: github.Ptr(9),
					Head: &github.PullRequestBranch{
						SHA:  github.Ptr("abc"),
						Repo: &github.Repository{Owner: &github.User{Login: github.Ptr("owner")}},
					},
				}, nil, nil).Once()
			}
			c := &Client{
				cfg:          config.ServerConfig{ReplanCommentMessage: "kubechecks again"},
				googleClient: &GClient{PullRequests: mockPR},
			}

			pr, err := parseHook(t, c, "issue_comment", tc.body)
			if !tc.handled {
				assert.ErrorIs(t, err, vcs.ErrInvalidType)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 9, pr.CheckID)
		})
	}
}
