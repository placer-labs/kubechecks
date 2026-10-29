package github_client

import (
	"context"
	"iter"

	"github.com/google/go-github/v93/github"
)

type RepositoriesServices interface {
	GetContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (fileContent *github.RepositoryContent, directoryContent []*github.RepositoryContent, resp *github.Response, err error)
	Get(ctx context.Context, owner, repo string) (*github.Repository, *github.Response, error)
	CreateStatus(ctx context.Context, owner, repo, ref string, status github.RepoStatus) (*github.RepoStatus, *github.Response, error)
	CreateHook(ctx context.Context, owner, repo string, hook *github.Hook) (*github.Hook, *github.Response, error)
	ListHooksIter(ctx context.Context, owner, repo string, opts *github.ListOptions) iter.Seq2[*github.Hook, error]
}

type RepositoriesService struct {
	RepositoriesServices
}
