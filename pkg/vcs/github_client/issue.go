package github_client

import (
	"context"
	"iter"

	"github.com/google/go-github/v93/github"
)

type IssuesServices interface {
	CreateComment(ctx context.Context, owner string, repo string, number int, body github.IssueCommentRequest) (*github.IssueComment, *github.Response, error)
	DeleteComment(ctx context.Context, owner string, repo string, commentID int64) (*github.Response, error)
	ListCommentsIter(ctx context.Context, owner string, repo string, number int, opts *github.IssueListCommentsOptions) iter.Seq2[*github.IssueComment, error]
	UpdateComment(ctx context.Context, owner string, repo string, commentID int64, body github.IssueCommentRequest) (*github.IssueComment, *github.Response, error)
}

type IssuesService struct {
	IssuesServices
}
