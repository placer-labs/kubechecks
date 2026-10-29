package github_client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"
	"github.com/shurcooL/githubv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

type fakeIssues struct {
	IssuesServices

	edited  map[int64]string
	created []string
	// status codes to fail the next calls with, 0 for no response at all
	editFailures, createFailures []int
}

func failNext(failures *[]int) (*github.Response, error) {
	if len(*failures) == 0 {
		return nil, nil
	}
	code := (*failures)[0]
	*failures = (*failures)[1:]
	if code == 0 {
		return nil, errors.New("connection reset")
	}
	return &github.Response{Response: &http.Response{StatusCode: code}}, errors.New(http.StatusText(code))
}

func (f *fakeIssues) UpdateComment(_ context.Context, _, _ string, id int64, body github.IssueCommentRequest) (*github.IssueComment, *github.Response, error) {
	if resp, err := failNext(&f.editFailures); err != nil {
		return nil, resp, err
	}
	if f.edited == nil {
		f.edited = map[int64]string{}
	}
	f.edited[id] = body.Body
	return &github.IssueComment{ID: github.Ptr(id), Body: github.Ptr(body.Body)}, nil, nil
}

func (f *fakeIssues) CreateComment(_ context.Context, _, _ string, _ int, body github.IssueCommentRequest) (*github.IssueComment, *github.Response, error) {
	if resp, err := failNext(&f.createFailures); err != nil {
		return nil, resp, err
	}
	f.created = append(f.created, body.Body)
	return &github.IssueComment{Body: github.Ptr(body.Body)}, nil, nil
}

func newMessageClient(issues *fakeIssues) *Client {
	return &Client{googleClient: &GClient{Issues: issues}, commentRetry: fastRetry}
}

var testPR = vcs.PullRequest{Owner: "zapier", Name: "kubechecks", FullName: "zapier/kubechecks", CheckID: 7}

func updateMessage(t *testing.T, issues *fakeIssues, chunks ...string) error {
	t.Helper()
	return newMessageClient(issues).UpdateMessage(context.Background(), testPR, 42, chunks)
}

func TestUpdateMessage_SingleChunkEditsInPlace(t *testing.T) {
	issues := &fakeIssues{}

	require.NoError(t, updateMessage(t, issues, "report"))

	assert.Equal(t, map[int64]string{42: "report"}, issues.edited)
	assert.Empty(t, issues.created)
}

func TestUpdateMessage_OverflowIsPostedInOrder(t *testing.T) {
	issues := &fakeIssues{}

	require.NoError(t, updateMessage(t, issues, "one", "two", "three"))

	assert.Equal(t, map[int64]string{42: "one"}, issues.edited)
	assert.Equal(t, []string{"two", "three"}, issues.created)
}

func TestUpdateMessage_Retries(t *testing.T) {
	issues := &fakeIssues{
		editFailures:   []int{0, http.StatusBadGateway},
		createFailures: []int{http.StatusTooManyRequests, http.StatusTooManyRequests},
	}

	require.NoError(t, updateMessage(t, issues, "one", "two"))

	assert.Equal(t, map[int64]string{42: "one"}, issues.edited)
	assert.Equal(t, []string{"two"}, issues.created)
}

func TestUpdateMessage_GivesUp(t *testing.T) {
	for name, tc := range map[string]struct {
		issues *fakeIssues
		want   string
	}{
		"client error on edit":   {&fakeIssues{editFailures: []int{http.StatusUnprocessableEntity, http.StatusBadGateway}}, "posting comment 1 of 2"},
		"client error on create": {&fakeIssues{createFailures: []int{http.StatusUnprocessableEntity, http.StatusBadGateway}}, "posting comment 2 of 2"},
		// the comment may be there already
		"no response to a create":  {&fakeIssues{createFailures: []int{0, http.StatusBadGateway}}, "posting comment 2 of 2"},
		"server error on a create": {&fakeIssues{createFailures: []int{http.StatusServiceUnavailable, http.StatusBadGateway}}, "posting comment 2 of 2"},
	} {
		t.Run(name, func(t *testing.T) {
			err := updateMessage(t, tc.issues, "one", "two")

			require.ErrorContains(t, err, tc.want)
			// a second attempt would have used up the 502
			assert.Equal(t, []int{http.StatusBadGateway}, append(tc.issues.editFailures, tc.issues.createFailures...))
			assert.Empty(t, tc.issues.created)
		})
	}
}

func TestUpdateMessage_TrimsOversizedChunk(t *testing.T) {
	issues := &fakeIssues{}

	require.NoError(t, updateMessage(t, issues, strings.Repeat("x", MaxCommentLength+10)))

	assert.Len(t, issues.edited[42], MaxCommentLength)
}

type fakeCommentList struct {
	IssuesServices

	pages   [][]*github.IssueComment
	perPage []int
	deleted []int64
}

func (f *fakeCommentList) ListCommentsIter(_ context.Context, _, _ string, _ int, opts *github.IssueListCommentsOptions) iter.Seq2[*github.IssueComment, error] {
	return func(yield func(*github.IssueComment, error) bool) {
		for _, page := range f.pages {
			f.perPage = append(f.perPage, opts.PerPage)
			for _, c := range page {
				if !yield(c, nil) {
					return
				}
			}
		}
	}
}

func (f *fakeCommentList) DeleteComment(_ context.Context, _, _ string, id int64) (*github.Response, error) {
	f.deleted = append(f.deleted, id)
	return nil, nil
}

type graphqlRecorder struct {
	minimized map[string]bool
	lookups   [][]string
	mutated   []string
}

func (g *graphqlRecorder) serve(t *testing.T) *githubv4.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string `json:"query"`
			Variables struct {
				IDs   []string `json:"ids"`
				Input struct {
					SubjectID string `json:"subjectId"`
				} `json:"input"`
			} `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		switch {
		case strings.Contains(req.Query, "nodes(ids: $ids)"):
			g.lookups = append(g.lookups, req.Variables.IDs)
			nodes := make([]map[string]any, 0, len(req.Variables.IDs))
			for _, id := range req.Variables.IDs {
				nodes = append(nodes, map[string]any{"id": id, "isMinimized": g.minimized[id]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"nodes": nodes}})
		case strings.Contains(req.Query, "minimizeComment"):
			g.mutated = append(g.mutated, req.Variables.Input.SubjectID)
			_, _ = w.Write([]byte(`{"data":{"minimizeComment":{"minimizedComment":{"isMinimized":true}}}}`))
		default:
			t.Errorf("unexpected graphql query: %s", req.Query)
		}
	}))
	t.Cleanup(srv.Close)
	return githubv4.NewEnterpriseClient(srv.URL, srv.Client())
}

func botComment(id int64, login string) *github.IssueComment {
	return &github.IssueComment{
		ID:     github.Ptr(id),
		NodeID: github.Ptr(fmt.Sprintf("IC_%d", id)),
		User:   &github.User{Login: github.Ptr(login)},
		Body:   github.Ptr("report"),
	}
}

func TestTidyOutdatedComments_SkipsAlreadyMinimized(t *testing.T) {
	issues := &fakeCommentList{pages: [][]*github.IssueComment{
		{botComment(1, "kubechecks-bot"), botComment(2, "someone-else"), {ID: github.Ptr(int64(3))}},
		{botComment(4, "kubechecks-bot"), botComment(5, "KUBECHECKS-BOT")},
	}}
	gql := &graphqlRecorder{minimized: map[string]bool{"IC_1": true, "IC_4": true}}

	c := &Client{
		googleClient:   &GClient{Issues: issues},
		shurcoolClient: gql.serve(t),
		username:       "kubechecks-bot",
	}

	require.NoError(t, c.TidyOutdatedComments(context.Background(), testPR))

	assert.Equal(t, []int{100, 100}, issues.perPage)
	assert.Equal(t, [][]string{{"IC_1", "IC_4", "IC_5"}}, gql.lookups)
	assert.Equal(t, []string{"IC_5"}, gql.mutated)
	assert.Empty(t, issues.deleted)
}

func TestTidyOutdatedComments_MatchesReportByIdentifier(t *testing.T) {
	report := botComment(6, "a-human")
	report.Body = github.Ptr("## Kubechecks prod Report\n...")
	issues := &fakeCommentList{pages: [][]*github.IssueComment{{report, botComment(7, "a-human")}}}
	gql := &graphqlRecorder{}

	c := &Client{
		googleClient:   &GClient{Issues: issues},
		shurcoolClient: gql.serve(t),
		username:       "kubechecks-bot",
		cfg:            config.ServerConfig{Identifier: "prod"},
	}

	require.NoError(t, c.TidyOutdatedComments(context.Background(), testPR))

	assert.Equal(t, []string{"IC_6"}, gql.mutated)
}

func TestTidyOutdatedComments_LooksUpMinimizedStateInBatches(t *testing.T) {
	var comments []*github.IssueComment
	for i := range 150 {
		comments = append(comments, botComment(int64(i+1), "kubechecks-bot"))
	}
	issues := &fakeCommentList{pages: [][]*github.IssueComment{comments}}
	gql := &graphqlRecorder{minimized: map[string]bool{}}
	for i := range 149 {
		gql.minimized[fmt.Sprintf("IC_%d", i+1)] = true
	}

	c := &Client{
		googleClient:   &GClient{Issues: issues},
		shurcoolClient: gql.serve(t),
		username:       "kubechecks-bot",
	}

	require.NoError(t, c.TidyOutdatedComments(context.Background(), testPR))

	require.Len(t, gql.lookups, 2)
	assert.Len(t, gql.lookups[0], 100)
	assert.Len(t, gql.lookups[1], 50)
	assert.Equal(t, []string{"IC_150"}, gql.mutated)
}

func TestTidyOutdatedComments_NoCandidatesSkipsGraphQL(t *testing.T) {
	issues := &fakeCommentList{pages: [][]*github.IssueComment{{botComment(1, "someone-else")}}}

	c := &Client{googleClient: &GClient{Issues: issues}, username: "kubechecks-bot"}

	require.NoError(t, c.TidyOutdatedComments(context.Background(), testPR))
}

func TestTidyOutdatedComments_DeleteMode(t *testing.T) {
	issues := &fakeCommentList{pages: [][]*github.IssueComment{
		{botComment(1, "kubechecks-bot"), botComment(2, "someone-else"), {ID: github.Ptr(int64(3))}},
	}}

	c := &Client{
		googleClient: &GClient{Issues: issues},
		username:     "kubechecks-bot",
		cfg:          config.ServerConfig{TidyOutdatedCommentsMode: "delete"},
	}

	require.NoError(t, c.TidyOutdatedComments(context.Background(), testPR))

	assert.Equal(t, []int64{1}, issues.deleted)
}
