package github_client

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func testAppKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func enterpriseServer(t *testing.T, handler http.HandlerFunc) config.ServerConfig {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return config.ServerConfig{VcsBaseUrl: srv.URL + "/", VcsUploadUrl: srv.URL + "/"}
}

func appConfig(t *testing.T, cfg config.ServerConfig) config.ServerConfig {
	cfg.GithubAppID = 1234
	cfg.GithubInstallationID = 5678
	cfg.GithubPrivateKey = testAppKey(t)
	return cfg
}

func TestCreateGithubClient_GitHubAppUsesBotLogin(t *testing.T) {
	var paths []string
	cfg := appConfig(t, enterpriseServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/v3/app" {
			assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
			_, _ = w.Write([]byte(`{"id":1234,"slug":"my-checks"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))

	c, err := CreateGithubClient(context.Background(), cfg)
	require.NoError(t, err)

	assert.Equal(t, "my-checks[bot]", c.Username())
	assert.Equal(t, vcs.DefaultVcsEmail, c.Email())
	assert.Equal(t, []string{"/api/v3/app"}, paths)
}

func TestCreateGithubClient_GitHubAppKeepsConfiguredUsername(t *testing.T) {
	cfg := appConfig(t, enterpriseServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	cfg.VcsUsername = "configured"

	c, err := CreateGithubClient(context.Background(), cfg)
	require.NoError(t, err)

	assert.Equal(t, "configured", c.Username())
}

func TestCreateGithubClient_GitHubAppLookupFailureFallsBack(t *testing.T) {
	cfg := appConfig(t, enterpriseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	c, err := CreateGithubClient(context.Background(), cfg)
	require.NoError(t, err)

	assert.Equal(t, vcs.DefaultVcsUsername, c.Username())
}

func TestCreateGithubClient_TokenUsesAuthenticatedUser(t *testing.T) {
	cfg := enterpriseServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/user", r.URL.Path)
		assert.Equal(t, "Bearer a-token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"login":"a-user","email":"a-user@example.com"}`))
	})
	cfg.VcsToken = "a-token"

	c, err := CreateGithubClient(context.Background(), cfg)
	require.NoError(t, err)

	assert.Equal(t, "a-user", c.Username())
	assert.Equal(t, "a-user@example.com", c.Email())
}
