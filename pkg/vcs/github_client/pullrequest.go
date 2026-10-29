package github_client

import (
	"context"
	"iter"

	"github.com/google/go-github/v93/github"
)

type PullRequestsServices interface {
	List(ctx context.Context, owner string, repo string, opts *github.PullRequestListOptions) ([]*github.PullRequest, *github.Response, error)
	ListFilesIter(ctx context.Context, owner string, repo string, number int, opts *github.ListOptions) iter.Seq2[*github.CommitFile, error]
	ListCommentsIter(ctx context.Context, owner string, repo string, number int, opts *github.PullRequestListCommentsOptions) iter.Seq2[*github.PullRequestComment, error]
	GetRaw(ctx context.Context, owner string, repo string, number int, opts github.RawOptions) (string, *github.Response, error)
	Get(ctx context.Context, owner string, repo string, number int) (*github.PullRequest, *github.Response, error)
	CreateReview(ctx context.Context, owner, repo string, number int, review *github.PullRequestReviewRequest) (*github.PullRequestReview, *github.Response, error)
	DeleteComment(ctx context.Context, owner, repo string, commentID int64) (*github.Response, error)
}

type PullRequestsService struct {
	PullRequestsServices
}
