package github_client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/shurcooL/githubv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	githubMocks "github.com/zapier/kubechecks/mocks/github_client/mocks"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

// MockGitHubMethod is a generic function to mock GitHub client methods
func MockGitHubMethod(methodName string, returns []interface{}) *GClient {
	mockClient := new(githubMocks.MockRepositoriesServices)
	mockClient.On(methodName, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(returns...)

	return &GClient{
		Repositories: mockClient,
	}
}

// MockGitHubPullRequestMethod is a generic function to mock GitHub client methods
func MockGitHubPullRequestMethod(methodName string, returns []interface{}) *GClient {
	mockClient := new(githubMocks.MockPullRequestsServices)
	mockClient.On(methodName, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(returns...)

	return &GClient{
		PullRequests: mockClient,
	}
}

func TestParseRepo(t *testing.T) {
	testcases := []struct {
		name, input                 string
		expectedOwner, expectedRepo string
	}{
		{
			name:          "github.com over ssh",
			input:         "git@github.com:zapier/kubechecks.git",
			expectedOwner: "zapier",
			expectedRepo:  "kubechecks",
		},
		{
			name:          "github.com over https",
			input:         "https://github.com/zapier/kubechecks.git",
			expectedOwner: "zapier",
			expectedRepo:  "kubechecks",
		},
		{
			name:          "github.com with https with username without .git",
			input:         "https://djeebus@github.com/zapier/kubechecks",
			expectedOwner: "zapier",
			expectedRepo:  "kubechecks",
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			owner, repo := parseRepo(tc.input)
			assert.Equal(t, tc.expectedOwner, owner)
			assert.Equal(t, tc.expectedRepo, repo)
		})
	}
}

func TestClient_CreateHook(t *testing.T) {
	type fields struct {
		shurcoolClient *githubv4.Client
		googleClient   *GClient
		cfg            config.ServerConfig
		username       string
		email          string
	}
	type args struct {
		ctx              context.Context
		ownerAndRepoName string
		webhookUrl       string
		webhookSecret    string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "normal ok",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("CreateHook",
					[]interface{}{
						&github.Hook{},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
				webhookSecret:    "dummy-webhook-secret",
			},
			wantErr: assert.NoError,
		},
		{
			name: "github responds with error",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("CreateHook",
					[]interface{}{
						nil,
						&github.Response{Response: &http.Response{StatusCode: http.StatusBadRequest}},
						fmt.Errorf("mock bad request")}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
				webhookSecret:    "dummy-webhook-secret",
			},
			wantErr: assert.Error,
		},
		{
			name: "mock network error error",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("CreateHook",
					[]interface{}{
						nil,
						nil,
						fmt.Errorf("mock network error")}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
				webhookSecret:    "dummy-webhook-secret",
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				shurcoolClient: tt.fields.shurcoolClient,
				googleClient:   tt.fields.googleClient,
				cfg:            tt.fields.cfg,
				username:       tt.fields.username,
				email:          tt.fields.email,
			}
			tt.wantErr(t, c.CreateHook(tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl, tt.args.webhookSecret), fmt.Sprintf("CreateHook(%v, %v, %v, %v)", tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl, tt.args.webhookSecret))
		})
	}
}

func TestClient_GetHookByUrl(t *testing.T) {
	type fields struct {
		shurcoolClient *githubv4.Client
		googleClient   *GClient
		cfg            config.ServerConfig
		username       string
		email          string
	}
	type args struct {
		ctx              context.Context
		ownerAndRepoName string
		webhookUrl       string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *vcs.WebHookConfig
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "normal ok",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						[]*github.Hook{
							{
								Config: &github.HookConfig{
									ContentType: github.Ptr("json"),
									InsecureSSL: github.Ptr("0"),
									URL:         github.Ptr("https://dummywebhooks.local"),
									Secret:      github.Ptr("dummy-webhook-secret"),
								},
								Events: []string{"pull_request"},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want: &vcs.WebHookConfig{
				Url:    "https://dummywebhooks.local",
				Events: []string{"pull_request"},
			},
			wantErr: assert.NoError,
		},
		{
			name: "no matching webhook found",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						[]*github.Hook{
							{
								Config: &github.HookConfig{
									ContentType: github.Ptr("json"),
									InsecureSSL: github.Ptr("0"),
									URL:         github.Ptr("https://differentwebhook.local"),
									Secret:      github.Ptr("dummy-webhook-secret"),
								},
								Events: []string{"pull_request"},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want:    nil,
			wantErr: assert.Error,
		},
		{
			name: "0 webhook found",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						nil,
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want:    nil,
			wantErr: assert.Error,
		},
		{
			name: "github error",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubMethod("ListHooks",
					[]interface{}{
						nil,
						&github.Response{Response: &http.Response{StatusCode: http.StatusBadRequest}},
						fmt.Errorf("mock bad request")}),
				cfg: config.ServerConfig{
					VcsToken: "ghp_helloworld",
					VcsType:  "github",
				},
				username: "dummy-bot",
				email:    "dummy@zapier.com",
			},
			args: args{
				ctx:              context.Background(),
				ownerAndRepoName: "https://dummy-bot:********@github.com/dummy-bot-zapier/test-repo.git",
				webhookUrl:       "https://dummywebhooks.local",
			},
			want:    nil,
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				shurcoolClient: tt.fields.shurcoolClient,
				googleClient:   tt.fields.googleClient,
				cfg:            tt.fields.cfg,
				username:       tt.fields.username,
				email:          tt.fields.email,
			}
			got, err := c.GetHookByUrl(tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl)
			if !tt.wantErr(t, err, fmt.Sprintf("GetHookByUrl(%v, %v, %v)", tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetHookByUrl(%v, %v, %v)", tt.args.ctx, tt.args.ownerAndRepoName, tt.args.webhookUrl)
		})
	}
}

func TestClient_buildRepoFromComment_HappyPath(t *testing.T) {
	type fields struct {
		shurcoolClient *githubv4.Client
		googleClient   *GClient
		cfg            config.ServerConfig
		username       string
		email          string
	}
	type args struct {
		context context.Context
		comment *github.IssueCommentEvent
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   vcs.PullRequest
	}{
		{
			name: "normal ok",
			fields: fields{
				shurcoolClient: nil,
				googleClient: MockGitHubPullRequestMethod("Get",
					[]interface{}{
						&github.PullRequest{
							ID:     nil,
							Number: github.Ptr(123),
							Labels: []*github.Label{
								{
									Name: github.Ptr("test label1"),
								},
								{
									Name: github.Ptr("test label2"),
								},
							},
							Head: &github.PullRequestBranch{
								Ref: github.Ptr("new-feature"),
								SHA: github.Ptr("dummySHAHead"),
								Repo: &github.Repository{
									CloneURL:      github.Ptr("https://github.com/fork/kubechecks/"),
									DefaultBranch: github.Ptr("fork-main"),
									FullName:      github.Ptr("fork/kubechecks"),
									Owner:         &github.User{Login: github.Ptr("fork")},
									Name:          github.Ptr("kubechecks"),
								},
							},
							Base: &github.PullRequestBranch{
								Ref: github.Ptr("main"),
								SHA: github.Ptr("dummySHABase"),
								Repo: &github.Repository{
									CloneURL:      github.Ptr("https://github.com/zapier/kubechecks/"),
									DefaultBranch: github.Ptr("main"),
									FullName:      github.Ptr("zapier/kubechecks"),
									Owner:         &github.User{Login: github.Ptr("zapier")},
									Name:          github.Ptr("kubechecks"),
								},
							},
						},
						&github.Response{Response: &http.Response{StatusCode: http.StatusOK}},
						nil},
				),
				cfg:      config.ServerConfig{},
				username: "unittestuser",
				email:    "unitestuser@localhost.local",
			},
			args: args{
				context: context.TODO(),
				comment: &github.IssueCommentEvent{
					Issue: &github.Issue{
						URL:    github.Ptr("https://github.com/zapier/kubechecks/pull/250"),
						Number: github.Ptr(250),
						Repository: &github.Repository{
							Name: github.Ptr("kubechecks"),
							Owner: &github.User{
								Name: github.Ptr("zapier"),
							},
						},
					},
					Repo: &github.Repository{
						DefaultBranch: github.Ptr("main"),
						Name:          github.Ptr("kubechecks"),
						FullName:      github.Ptr("zapier/kubechecks"),
					},
				},
			},
			want: vcs.PullRequest{
				BaseRef:       "main",
				HeadRef:       "dummySHAHead",
				DefaultBranch: "main",
				CloneURL:      "https://github.com/zapier/kubechecks/",
				Name:          "kubechecks",
				Owner:         "zapier",
				CheckID:       123,
				SHA:           "dummySHAHead",
				FullName:      "zapier/kubechecks",
				Username:      "unittestuser",
				Email:         "unitestuser@localhost.local",
				Labels:        []string{"test label1", "test label2"},
				Config:        config.ServerConfig{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				shurcoolClient: tt.fields.shurcoolClient,
				googleClient:   tt.fields.googleClient,
				cfg:            tt.fields.cfg,
				username:       tt.fields.username,
				email:          tt.fields.email,
			}
			actual, err := c.buildRepoFromComment(tt.args.context, tt.args.comment)
			require.NoError(t, err)
			assert.Equal(t, tt.want.Name, actual.Name)
			assert.Equal(t, tt.want.Labels, actual.Labels)
			assert.Equal(t, tt.want.CheckID, actual.CheckID)
			assert.Equal(t, tt.want.BaseRef, actual.BaseRef)
			assert.Equal(t, tt.want.CloneURL, actual.CloneURL)
			assert.Equal(t, tt.want.DefaultBranch, actual.DefaultBranch)
			assert.Equal(t, tt.want.Email, actual.Email)
			assert.Equal(t, tt.want.FullName, actual.FullName)
			assert.Equal(t, tt.want.HeadRef, actual.HeadRef)
			assert.Equal(t, tt.want.Owner, actual.Owner)
			assert.Equal(t, tt.want.Remote, actual.Remote)
			assert.Equal(t, tt.want.SHA, actual.SHA)
			assert.Equal(t, tt.want.Username, actual.Username)
		})
	}
}

func TestClient_SimpleGetters(t *testing.T) {
	t.Run("with token auth", func(t *testing.T) {
		c := &Client{
			cfg: config.ServerConfig{
				VcsUsername: "test-user",
				VcsEmail:    "test@example.com",
			},
			username: "test-user",
			email:    "test@example.com",
		}

		assert.Equal(t, "test-user", c.Username())
		assert.Equal(t, "test@example.com", c.Email())
		assert.Equal(t, "test-user", c.CloneUsername())
		assert.Equal(t, "github", c.GetName())
	})

	t.Run("with github app auth", func(t *testing.T) {
		c := &Client{
			cfg: config.ServerConfig{
				GithubAppID:          123,
				GithubInstallationID: 456,
				GithubPrivateKey:     "key",
			},
			username: "app-bot",
			email:    "app-bot@example.com",
		}

		assert.Equal(t, "x-access-token", c.CloneUsername())
	})
}

func TestClient_GetAuthHeaders(t *testing.T) {
	c := &Client{
		cfg: config.ServerConfig{
			VcsToken: "ghp_test_token_12345",
		},
	}

	headers, err := c.GetAuthHeaders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer ghp_test_token_12345", headers["Authorization"])
}

func TestClient_GetAuthHeaders_NoCredentials(t *testing.T) {
	// Neither PAT nor App transport configured — should error rather than emit
	// an empty `Bearer ` header that the VCS will reject.
	c := &Client{cfg: config.ServerConfig{}}

	_, err := c.GetAuthHeaders(context.Background())
	require.Error(t, err)
}

// stubTokenSource is a testing installationTokenSource that returns a fixed token
// (or error) without needing a real ghinstallation.Transport / JWT signer.
type stubTokenSource struct {
	token string
	err   error
}

func (s stubTokenSource) Token(_ context.Context) (string, error) {
	return s.token, s.err
}

func TestClient_GetAuthHeaders_GitHubApp(t *testing.T) {
	// In App mode VcsToken is empty; the Bearer token must come from the installation
	// token source, not the empty config field.
	c := &Client{
		cfg:            config.ServerConfig{},
		appTokenSource: stubTokenSource{token: "ghs_installation_token_xyz"},
	}

	headers, err := c.GetAuthHeaders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer ghs_installation_token_xyz", headers["Authorization"])
}

func TestClient_GetAuthHeaders_GitHubApp_TokenFetchError(t *testing.T) {
	// Token() failure (e.g. expired private key, network failure during JWT exchange)
	// must surface as an error rather than emit an empty Bearer header.
	c := &Client{
		cfg:            config.ServerConfig{},
		appTokenSource: stubTokenSource{err: fmt.Errorf("upstream JWT exchange failed")},
	}

	_, err := c.GetAuthHeaders(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "installation token")
}

func TestClient_GitCredentials_GitHubApp(t *testing.T) {
	// In App mode VcsToken is empty; git-over-HTTPS auth must use the "x-access-token"
	// username and a fresh installation token as the password.
	c := &Client{
		cfg:            config.ServerConfig{GithubAppID: 1, GithubInstallationID: 2, GithubPrivateKey: "stub-key"},
		appTokenSource: stubTokenSource{token: "ghs_installation_token_xyz"},
	}

	username, password, err := c.GitCredentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "x-access-token", username)
	assert.Equal(t, "ghs_installation_token_xyz", password)
}

func TestClient_GitCredentials_GitHubApp_TokenFetchError(t *testing.T) {
	// Token() failure (e.g. expired private key, network failure during JWT exchange)
	// must propagate rather than silently return empty credentials.
	c := &Client{
		cfg:            config.ServerConfig{GithubAppID: 1, GithubInstallationID: 2},
		appTokenSource: stubTokenSource{err: fmt.Errorf("upstream JWT exchange failed")},
	}

	_, _, err := c.GitCredentials(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "installation token")
}

func TestClient_GitCredentials_PAT(t *testing.T) {
	// In PAT mode there is no token source; credentials come from the static VcsToken
	// and the client's configured username.
	c := &Client{
		cfg:      config.ServerConfig{VcsToken: "ghp_static_pat"},
		username: "kubechecks-bot",
	}

	username, password, err := c.GitCredentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "kubechecks-bot", username)
	assert.Equal(t, "ghp_static_pat", password)
}

func TestClient_VerifyHook(t *testing.T) {
	tests := []struct {
		name     string
		secret   string
		payload  string
		setupReq func() *http.Request
		wantErr  bool
	}{
		{
			name:    "no secret - should pass",
			secret:  "",
			payload: "test payload",
			setupReq: func() *http.Request {
				req, _ := http.NewRequest("POST", "/webhook", strings.NewReader("test payload"))
				return req
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{}
			req := tt.setupReq()
			body, err := c.VerifyHook(req, tt.secret)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, body)
			}
		})
	}
}

func TestClient_buildRepo(t *testing.T) {
	sameRepo := &github.Repository{
		DefaultBranch: github.Ptr("main"),
		CloneURL:      github.Ptr("https://github.com/owner/repo.git"),
		FullName:      github.Ptr("owner/repo"),
		Name:          github.Ptr("repo"),
		Owner: &github.User{
			Login: github.Ptr("owner"),
		},
	}
	pr := &github.PullRequest{
		Number: github.Ptr(123),
		Head: &github.PullRequestBranch{
			Ref:  github.Ptr("feature-branch"),
			SHA:  github.Ptr("abc123"),
			Repo: sameRepo,
		},
		Base: &github.PullRequestBranch{
			Ref:  github.Ptr("main"),
			Repo: sameRepo,
		},
		Labels: []*github.Label{
			{Name: github.Ptr("bug")},
			{Name: github.Ptr("enhancement")},
		},
	}

	c := &Client{
		cfg:      config.ServerConfig{},
		username: "test-bot",
		email:    "test-bot@example.com",
	}

	result := c.buildRepo(pr)

	assert.Equal(t, "main", result.BaseRef)
	assert.Equal(t, "feature-branch", result.HeadRef)
	assert.Equal(t, "main", result.DefaultBranch)
	assert.Equal(t, "https://github.com/owner/repo.git", result.CloneURL)
	assert.Equal(t, "owner/repo", result.FullName)
	assert.Equal(t, "owner", result.Owner)
	assert.Equal(t, "repo", result.Name)
	assert.Equal(t, 123, result.CheckID)
	assert.Equal(t, "abc123", result.SHA)
	assert.Equal(t, "test-bot", result.Username)
	assert.Equal(t, "test-bot@example.com", result.Email)
	assert.Equal(t, []string{"bug", "enhancement"}, result.Labels)
}

func TestClient_buildRepo_ForkTargetsBaseRepo(t *testing.T) {
	pr := &github.PullRequest{
		Number: github.Ptr(77),
		Head: &github.PullRequestBranch{
			Ref: github.Ptr("contributor-change"),
			SHA: github.Ptr("forksha"),
			Repo: &github.Repository{
				DefaultBranch: github.Ptr("trunk"),
				CloneURL:      github.Ptr("https://github.com/contributor/repo.git"),
				FullName:      github.Ptr("contributor/repo"),
				Name:          github.Ptr("repo-fork"),
				Owner:         &github.User{Login: github.Ptr("contributor")},
				Fork:          github.Ptr(true),
			},
		},
		Base: &github.PullRequestBranch{
			Ref: github.Ptr("main"),
			SHA: github.Ptr("basesha"),
			Repo: &github.Repository{
				DefaultBranch: github.Ptr("main"),
				CloneURL:      github.Ptr("https://github.com/upstream/repo.git"),
				FullName:      github.Ptr("upstream/repo"),
				Name:          github.Ptr("repo"),
				Owner:         &github.User{Login: github.Ptr("upstream")},
			},
		},
	}

	result := (&Client{}).buildRepo(pr)

	assert.Equal(t, "upstream", result.Owner)
	assert.Equal(t, "repo", result.Name)
	assert.Equal(t, "upstream/repo", result.FullName)
	assert.Equal(t, "https://github.com/upstream/repo.git", result.CloneURL)
	assert.Equal(t, "main", result.DefaultBranch)
	assert.Equal(t, "main", result.BaseRef)
	assert.Equal(t, "forksha", result.HeadRef)
	assert.Equal(t, "forksha", result.SHA)
	assert.Equal(t, 77, result.CheckID)
}

func TestClient_buildRepo_ForkBranchNamedLikeBaseBranch(t *testing.T) {
	upstream := &github.Repository{
		CloneURL: github.Ptr("https://github.com/upstream/repo.git"),
		FullName: github.Ptr("upstream/repo"),
		Name:     github.Ptr("repo"),
		Owner:    &github.User{Login: github.Ptr("upstream")},
	}
	fork := &github.Repository{
		CloneURL: github.Ptr("https://github.com/contributor/repo.git"),
		FullName: github.Ptr("contributor/repo"),
		Name:     github.Ptr("repo"),
		Owner:    &github.User{Login: github.Ptr("contributor")},
	}

	for name, tc := range map[string]struct {
		head        *github.Repository
		headRef     string
		wantHeadRef string
	}{
		"fork branch matches the base branch":     {head: fork, headRef: "main", wantHeadRef: "forksha"},
		"fork branch matches another base branch": {head: fork, headRef: "release", wantHeadRef: "forksha"},
		"deleted fork":                     {head: nil, headRef: "release", wantHeadRef: "forksha"},
		"same repository keeps the branch": {head: upstream, headRef: "release", wantHeadRef: "release"},
	} {
		t.Run(name, func(t *testing.T) {
			pr := &github.PullRequest{
				Number: github.Ptr(5),
				Head:   &github.PullRequestBranch{Ref: github.Ptr(tc.headRef), SHA: github.Ptr("forksha"), Repo: tc.head},
				Base:   &github.PullRequestBranch{Ref: github.Ptr("main"), Repo: upstream},
			}

			result := (&Client{}).buildRepo(pr)

			assert.Equal(t, tc.wantHeadRef, result.HeadRef)
			assert.Equal(t, "https://github.com/upstream/repo.git", result.CloneURL)
		})
	}
}

func TestClient_buildRepo_MissingRepos(t *testing.T) {
	result := (&Client{}).buildRepo(&github.PullRequest{Number: github.Ptr(1)})

	assert.Equal(t, 1, result.CheckID)
	assert.Empty(t, result.Owner)
	assert.Empty(t, result.SHA)
}

func TestParseHook_ForkPullRequestTargetsBaseRepo(t *testing.T) {
	body := `{"action":"opened","pull_request":{"number":12,
		"head":{"ref":"patch-1","sha":"forksha","repo":{"name":"repo","full_name":"contributor/repo","clone_url":"https://github.com/contributor/repo.git","default_branch":"main","fork":true,"owner":{"login":"contributor"}}},
		"base":{"ref":"main","sha":"basesha","repo":{"name":"repo","full_name":"upstream/repo","clone_url":"https://github.com/upstream/repo.git","default_branch":"main","owner":{"login":"upstream"}}}}}`

	pr, err := parseHook(t, &Client{}, "pull_request", body)
	require.NoError(t, err)

	assert.Equal(t, "upstream", pr.Owner)
	assert.Equal(t, "upstream/repo", pr.FullName)
	assert.Equal(t, "https://github.com/upstream/repo.git", pr.CloneURL)
	assert.Equal(t, "forksha", pr.HeadRef)
	assert.Equal(t, "forksha", pr.SHA)
}

func TestClient_buildRepoFromEvent(t *testing.T) {
	event := &github.PullRequestEvent{
		PullRequest: &github.PullRequest{
			Number: github.Ptr(456),
			Head: &github.PullRequestBranch{
				Ref:  github.Ptr("fix-bug"),
				SHA:  github.Ptr("def456"),
				Repo: &github.Repository{FullName: github.Ptr("org/project")},
			},
			Base: &github.PullRequestBranch{
				Ref: github.Ptr("develop"),
				Repo: &github.Repository{
					DefaultBranch: github.Ptr("develop"),
					CloneURL:      github.Ptr("https://github.com/org/project.git"),
					FullName:      github.Ptr("org/project"),
					Name:          github.Ptr("project"),
					Owner: &github.User{
						Login: github.Ptr("org"),
					},
				},
			},
			Labels: []*github.Label{
				{Name: github.Ptr("priority-high")},
			},
		},
	}

	c := &Client{
		cfg:      config.ServerConfig{},
		username: "event-bot",
		email:    "event-bot@example.com",
	}

	result := c.buildRepoFromEvent(event)

	assert.Equal(t, "develop", result.BaseRef)
	assert.Equal(t, "fix-bug", result.HeadRef)
	assert.Equal(t, 456, result.CheckID)
	assert.Equal(t, []string{"priority-high"}, result.Labels)
}

func TestToGithubCommitStatus(t *testing.T) {
	tests := []struct {
		name     string
		state    pkg.CommitState
		expected string
	}{
		{
			name:     "error state",
			state:    pkg.StateError,
			expected: "error",
		},
		{
			name:     "panic state",
			state:    pkg.StatePanic,
			expected: "error",
		},
		{
			name:     "failure state",
			state:    pkg.StateFailure,
			expected: "failure",
		},
		{
			name:     "running state",
			state:    pkg.StateRunning,
			expected: "pending",
		},
		{
			name:     "success state",
			state:    pkg.StateSuccess,
			expected: "success",
		},
		{
			name:     "warning state",
			state:    pkg.StateWarning,
			expected: "success",
		},
		{
			name:     "none state",
			state:    pkg.StateNone,
			expected: "success",
		},
		{
			name:     "skip state",
			state:    pkg.StateSkip,
			expected: "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toGithubCommitStatus(tt.state)
			assert.Equal(t, tt.expected, *result)
		})
	}
}

func TestClient_LoadHook_InvalidFormat(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "invalid format - no #",
			id:      "owner/repo/123",
			wantErr: true,
		},
		{
			name:    "invalid format - invalid number",
			id:      "owner/repo#abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				googleClient: &GClient{},
			}

			_, err := c.LoadHook(context.Background(), tt.id)
			if tt.wantErr {
				assert.Error(t, err)
			}
		})
	}
}

func TestUnPtr(t *testing.T) {
	t.Run("string pointer", func(t *testing.T) {
		str := "test"
		result := unPtr(&str)
		assert.Equal(t, "test", result)
	})

	t.Run("nil string pointer", func(t *testing.T) {
		var str *string
		result := unPtr(str)
		assert.Equal(t, "", result)
	})

	t.Run("int pointer", func(t *testing.T) {
		num := 42
		result := unPtr(&num)
		assert.Equal(t, 42, result)
	})

	t.Run("nil int pointer", func(t *testing.T) {
		var num *int
		result := unPtr(num)
		assert.Equal(t, 0, result)
	})
}

func TestClient_GetPullRequestFiles(t *testing.T) {
	mockPR := new(githubMocks.MockPullRequestsServices)

	// First page
	mockPR.On("ListFiles",
		mock.Anything,
		"owner",
		"repo",
		123,
		mock.MatchedBy(func(opts *github.ListOptions) bool {
			return opts.Page == 0 || opts.Page == 1
		})).Return(
		[]*github.CommitFile{
			{Filename: github.Ptr("file1.go")},
			{Filename: github.Ptr("file2.go")},
		},
		&github.Response{
			NextPage: 2,
			Response: &http.Response{StatusCode: 200},
		},
		nil,
	).Once()

	// Second page
	mockPR.On("ListFiles",
		mock.Anything,
		"owner",
		"repo",
		123,
		mock.MatchedBy(func(opts *github.ListOptions) bool {
			return opts.Page == 2
		})).Return(
		[]*github.CommitFile{
			{Filename: github.Ptr("file3.go")},
		},
		&github.Response{
			NextPage: 0,
			Response: &http.Response{StatusCode: 200},
		},
		nil,
	).Once()

	c := &Client{
		googleClient: &GClient{
			PullRequests: mockPR,
		},
	}

	pr := vcs.PullRequest{
		Owner:   "owner",
		Name:    "repo",
		CheckID: 123,
	}

	files, err := c.GetPullRequestFiles(context.Background(), pr)
	assert.NoError(t, err)
	assert.Len(t, files, 3)
	assert.Contains(t, files, "file1.go")
	assert.Contains(t, files, "file2.go")
	assert.Contains(t, files, "file3.go")

	mockPR.AssertExpectations(t)
}

func TestClient_CommitStatus(t *testing.T) {
	tests := []struct {
		name          string
		state         pkg.CommitState
		expectedState string
	}{
		{
			name:          "error state maps to error",
			state:         pkg.StateError,
			expectedState: "error",
		},
		{
			name:          "failure state maps to failure",
			state:         pkg.StateFailure,
			expectedState: "failure",
		},
		{
			name:          "running state maps to pending",
			state:         pkg.StateRunning,
			expectedState: "pending",
		},
		{
			name:          "success state maps to success",
			state:         pkg.StateSuccess,
			expectedState: "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepos := new(githubMocks.MockRepositoriesServices)
			mockRepos.On("CreateStatus",
				mock.Anything,
				"owner",
				"repo",
				"abc123",
				mock.MatchedBy(func(status *github.RepoStatus) bool {
					return status.State != nil && *status.State == tt.expectedState
				})).Return(
				&github.RepoStatus{},
				&github.Response{Response: &http.Response{StatusCode: 200}},
				nil,
			)

			c := &Client{
				googleClient: &GClient{
					Repositories: mockRepos,
				},
			}

			pr := vcs.PullRequest{
				Owner:   "owner",
				Name:    "repo",
				SHA:     "abc123",
				CheckID: 123,
			}

			err := c.CommitStatus(context.Background(), pr, tt.state)
			assert.NoError(t, err)
			mockRepos.AssertExpectations(t)
		})
	}
}

func TestParseRepo_InvalidURL(t *testing.T) {
	// parseRepo panics on invalid URLs
	assert.Panics(t, func() {
		parseRepo("not a valid url")
	})
}

func TestParseRepo_InvalidPath(t *testing.T) {
	// parseRepo panics on URLs with invalid path structure
	assert.Panics(t, func() {
		parseRepo("https://github.com/invalid")
	})
}

func TestClient_DownloadArchive_HappyPath(t *testing.T) {
	// GitHub returns a fully ready PR on the first call:
	// head SHA matches, merge_commit_sha present, mergeable is non-nil.
	mockPR := NewMockPullRequestsServicesWithGet(t,
		prResponse{
			headSHA:        "new-sha",
			mergeCommitSHA: "merge-abc123",
			mergeable:      github.Ptr(true),
		},
	)

	c := &Client{
		googleClient: &GClient{PullRequests: mockPR},
		archiveRetry: fastRetry,
	}

	pr := vcs.PullRequest{
		Owner:    "owner",
		Name:     "repo",
		FullName: "owner/repo",
		CheckID:  42,
		SHA:      "new-sha",
	}

	url, err := c.DownloadArchive(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, "https://api.github.com/repos/owner/repo/zipball/merge-abc123", url)
	mockPR.AssertExpectations(t)
}

func TestClient_DownloadArchive_StaleMergeCommitThenReady(t *testing.T) {
	// Simulates the race condition: a new commit is pushed, GitHub updates
	// head.sha immediately but merge_commit_sha is stale (from old merge)
	// and Mergeable is nil while GitHub recomputes.
	// First call: head SHA matches, stale merge_commit_sha present, Mergeable nil.
	// Second call: head SHA matches, new merge_commit_sha, Mergeable non-nil.
	mockPR := NewMockPullRequestsServicesWithGet(t,
		prResponse{
			headSHA:        "new-sha",
			mergeCommitSHA: "stale-old-merge",
			mergeable:      nil, // still computing
		},
		prResponse{
			headSHA:        "new-sha",
			mergeCommitSHA: "fresh-new-merge",
			mergeable:      github.Ptr(true),
		},
	)

	c := &Client{
		googleClient: &GClient{PullRequests: mockPR},
		archiveRetry: fastRetry,
	}

	pr := vcs.PullRequest{
		Owner:    "owner",
		Name:     "repo",
		FullName: "owner/repo",
		CheckID:  42,
		SHA:      "new-sha",
	}

	url, err := c.DownloadArchive(context.Background(), pr)
	require.NoError(t, err)
	// Must use the fresh merge SHA, not the stale one
	assert.Equal(t, "https://api.github.com/repos/owner/repo/zipball/fresh-new-merge", url)
	assert.NotContains(t, url, "stale-old-merge")
	mockPR.AssertExpectations(t)
}

func TestClient_DownloadArchive_NotMergeable(t *testing.T) {
	// GitHub has finished computing and the PR has conflicts.
	// Should short-circuit immediately without retrying.
	mockPR := NewMockPullRequestsServicesWithGet(t,
		prResponse{
			headSHA:        "new-sha",
			mergeCommitSHA: "", // no merge commit when conflicts exist
			mergeable:      github.Ptr(false),
		},
	)

	c := &Client{
		googleClient: &GClient{PullRequests: mockPR},
		archiveRetry: fastRetry,
	}

	pr := vcs.PullRequest{
		Owner:    "owner",
		Name:     "repo",
		FullName: "owner/repo",
		CheckID:  42,
		SHA:      "new-sha",
	}

	_, err := c.DownloadArchive(context.Background(), pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not mergeable")
	// Only 1 call - no retries after seeing Mergeable=false
	mockPR.AssertNumberOfCalls(t, "Get", 1)
}

func TestClient_DownloadArchive_ContextCancelled(t *testing.T) {
	// If merge stays not-ready and context is cancelled, should return context error.
	mockPR := new(githubMocks.MockPullRequestsServices)
	mockPR.On("Get", mock.Anything, "owner", "repo", 42).Return(
		&github.PullRequest{
			Head:           &github.PullRequestBranch{SHA: github.Ptr("new-sha")},
			MergeCommitSHA: github.Ptr("stale"),
			Mergeable:      nil,
		},
		&github.Response{Response: &http.Response{StatusCode: 200}},
		nil,
	).Once()

	c := &Client{
		googleClient: &GClient{PullRequests: mockPR},
		archiveRetry: fastRetry,
	}

	pr := vcs.PullRequest{
		Owner:    "owner",
		Name:     "repo",
		FullName: "owner/repo",
		CheckID:  42,
		SHA:      "new-sha",
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the backoff select picks it up
	cancel()

	_, err := c.DownloadArchive(ctx, pr)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	mockPR.AssertExpectations(t)
}

func TestClient_DownloadArchive_GHEnterprise(t *testing.T) {
	// Verify enterprise URL format.
	mockPR := NewMockPullRequestsServicesWithGet(t,
		prResponse{
			headSHA:        "sha1",
			mergeCommitSHA: "merge-sha",
			mergeable:      github.Ptr(true),
		},
	)

	c := &Client{
		googleClient: &GClient{PullRequests: mockPR},
		cfg: config.ServerConfig{
			VcsBaseUrl:   "https://github.example.com/api/v3",
			VcsUploadUrl: "https://github.example.com/api/uploads",
		},
		archiveRetry: fastRetry,
	}

	pr := vcs.PullRequest{
		Owner:    "myorg",
		Name:     "myrepo",
		FullName: "myorg/myrepo",
		CheckID:  1,
		SHA:      "sha1",
	}

	url, err := c.DownloadArchive(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, "https://github.example.com/api/v3/repos/myorg/myrepo/zipball/merge-sha", url)
	mockPR.AssertExpectations(t)
}

func TestClient_DownloadArchive_DefaultedVcsBaseUrl(t *testing.T) {
	// pkg/config defaults VcsBaseUrl to "https://github.com" when unset, so a cloud
	// configuration arrives at DownloadArchive with a non-empty VcsBaseUrl. The cloud
	// branch must still fire (api.github.com), not the enterprise branch which would
	// build a bogus `https://github.com/repos/...` URL. See issue #525 review.
	mockPR := NewMockPullRequestsServicesWithGet(t,
		prResponse{
			headSHA:        "sha1",
			mergeCommitSHA: "merge-sha",
			mergeable:      github.Ptr(true),
		},
	)

	c := &Client{
		googleClient: &GClient{PullRequests: mockPR},
		cfg:          config.ServerConfig{VcsBaseUrl: "https://github.com"}, // no VcsUploadUrl
		archiveRetry: fastRetry,
	}

	pr := vcs.PullRequest{
		Owner:    "owner",
		Name:     "repo",
		FullName: "owner/repo",
		CheckID:  1,
		SHA:      "sha1",
	}

	url, err := c.DownloadArchive(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, "https://api.github.com/repos/owner/repo/zipball/merge-sha", url)
	mockPR.AssertExpectations(t)
}

// --- test helpers ---

// fastRetry returns a retryConfig with near-zero delays for tests.
var fastRetry = retryConfig{
	maxRetries:     10,
	initialBackoff: 1 * time.Millisecond,
	maxBackoff:     1 * time.Millisecond,
}

type prResponse struct {
	headSHA        string
	mergeCommitSHA string
	mergeable      *bool
}

// NewMockPullRequestsServicesWithGet creates a mock that returns the given
// PR responses in order, one per call to Get.
func NewMockPullRequestsServicesWithGet(t *testing.T, responses ...prResponse) *githubMocks.MockPullRequestsServices {
	t.Helper()
	mockPR := new(githubMocks.MockPullRequestsServices)

	for _, resp := range responses {
		pr := &github.PullRequest{
			Head:      &github.PullRequestBranch{SHA: github.Ptr(resp.headSHA)},
			Mergeable: resp.mergeable,
		}
		if resp.mergeCommitSHA != "" {
			pr.MergeCommitSHA = github.Ptr(resp.mergeCommitSHA)
		}
		mockPR.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
			pr,
			&github.Response{Response: &http.Response{StatusCode: 200}},
			nil,
		).Once()
	}

	return mockPR
}

func listedFiles(n int) []*github.CommitFile {
	files := make([]*github.CommitFile, n)
	for i := range files {
		files[i] = &github.CommitFile{Filename: github.Ptr(fmt.Sprintf("file%d.yaml", i))}
	}
	return files
}

func TestClient_GetPullRequestFiles_Truncated(t *testing.T) {
	for name, tc := range map[string]struct {
		listed, changed int
		wantErr         string
	}{
		"truncated at the cap":  {listed: maxListedPullRequestFiles, changed: 3500, wantErr: "PR changes 3500 files but GitHub only lists the first 3000"},
		"exactly at the cap":    {listed: maxListedPullRequestFiles, changed: maxListedPullRequestFiles},
		"under the cap no call": {listed: 2999, changed: -1},
	} {
		t.Run(name, func(t *testing.T) {
			mockPR := githubMocks.NewMockPullRequestsServices(t)
			mockPR.On("ListFiles", mock.Anything, "owner", "repo", 123,
				mock.MatchedBy(func(opts *github.ListOptions) bool { return opts.PerPage == 100 }),
			).Return(listedFiles(tc.listed), &github.Response{Response: &http.Response{StatusCode: 200}}, nil).Once()
			if tc.changed >= 0 {
				mockPR.On("Get", mock.Anything, "owner", "repo", 123).
					Return(&github.PullRequest{ChangedFiles: github.Ptr(tc.changed)}, nil, nil).Once()
			}

			c := &Client{googleClient: &GClient{PullRequests: mockPR}}
			files, err := c.GetPullRequestFiles(context.Background(), vcs.PullRequest{Owner: "owner", Name: "repo", CheckID: 123})

			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				assert.Nil(t, files)
				return
			}
			require.NoError(t, err)
			assert.Len(t, files, tc.listed)
		})
	}
}

func TestClient_LoadHook_ForkUsesHeadSHA(t *testing.T) {
	mockRepos := githubMocks.NewMockRepositoriesServices(t)
	mockRepos.On("Get", mock.Anything, "upstream", "repo").Return(&github.Repository{
		FullName: github.Ptr("upstream/repo"),
		Name:     github.Ptr("repo"),
		Owner:    &github.User{Login: github.Ptr("upstream")},
	}, nil, nil).Once()
	mockPR := githubMocks.NewMockPullRequestsServices(t)
	mockPR.On("Get", mock.Anything, "upstream", "repo", 5).Return(&github.PullRequest{
		Number: github.Ptr(5),
		Head: &github.PullRequestBranch{Ref: github.Ptr("main"), SHA: github.Ptr("forksha"),
			Repo: &github.Repository{FullName: github.Ptr("contributor/repo")}},
		Base: &github.PullRequestBranch{Ref: github.Ptr("main"),
			Repo: &github.Repository{FullName: github.Ptr("upstream/repo")}},
	}, nil, nil).Once()

	c := &Client{googleClient: &GClient{Repositories: mockRepos, PullRequests: mockPR}}
	pr, err := c.LoadHook(context.Background(), "upstream/repo#5")
	require.NoError(t, err)

	assert.Equal(t, "forksha", pr.HeadRef)
	assert.Equal(t, "main", pr.BaseRef)
	assert.Equal(t, "upstream", pr.Owner)
}

func TestClient_GetHookByUrl_Paginates(t *testing.T) {
	hook := func(url string) *github.Hook {
		return &github.Hook{Config: &github.HookConfig{URL: github.Ptr(url)}, Events: []string{"pull_request"}}
	}
	page := func(next int) *github.Response {
		return &github.Response{NextPage: next, Response: &http.Response{StatusCode: http.StatusOK}}
	}
	onPage := func(n int) interface{} {
		return mock.MatchedBy(func(opts *github.ListOptions) bool { return opts.PerPage == 100 && opts.Page == n })
	}

	t.Run("found on a later page", func(t *testing.T) {
		mockRepos := githubMocks.NewMockRepositoriesServices(t)
		mockRepos.On("ListHooks", mock.Anything, "owner", "repo", onPage(0)).Return([]*github.Hook{hook("https://other.local")}, page(2), nil).Once()
		mockRepos.On("ListHooks", mock.Anything, "owner", "repo", onPage(2)).Return([]*github.Hook{hook("https://hooks.local")}, page(0), nil).Once()

		c := &Client{googleClient: &GClient{Repositories: mockRepos}}
		got, err := c.GetHookByUrl(context.Background(), "https://github.com/owner/repo.git", "https://hooks.local")

		require.NoError(t, err)
		assert.Equal(t, "https://hooks.local", got.Url)
	})

	t.Run("not found on any page", func(t *testing.T) {
		mockRepos := githubMocks.NewMockRepositoriesServices(t)
		mockRepos.On("ListHooks", mock.Anything, "owner", "repo", onPage(0)).Return([]*github.Hook{hook("https://other.local")}, page(2), nil).Once()
		mockRepos.On("ListHooks", mock.Anything, "owner", "repo", onPage(2)).Return([]*github.Hook{}, page(0), nil).Once()

		c := &Client{googleClient: &GClient{Repositories: mockRepos}}
		_, err := c.GetHookByUrl(context.Background(), "https://github.com/owner/repo.git", "https://hooks.local")

		assert.ErrorIs(t, err, vcs.ErrHookNotFound)
	})
}
