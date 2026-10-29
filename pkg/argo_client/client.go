package argo_client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/argoproj/argo-cd/v3/pkg/apiclient"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/applicationset"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/settings"
	repoapiclient "github.com/argoproj/argo-cd/v3/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v3/util/db"
	argosettings "github.com/argoproj/argo-cd/v3/util/settings"
	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	client "github.com/zapier/kubechecks/pkg/kubernetes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/argoproj/argo-cd/v3/pkg/apiclient/cluster"

	"github.com/zapier/kubechecks/pkg/config"
)

type ArgoClient struct {
	client    apiclient.Client
	k8s       kubernetes.Interface
	k8sConfig *rest.Config
	cfg       config.ServerConfig

	argoDBOnce  sync.Once
	settingsMgr *argosettings.SettingsManager
	argoDB      db.ArgoDB
}

func NewArgoClient(
	cfg config.ServerConfig,
	k8s client.Interface,
) (*ArgoClient, error) {
	opts := &apiclient.ClientOptions{
		ServerAddr:      cfg.ArgoCDServerAddr,
		AuthToken:       cfg.ArgoCDToken,
		GRPCWebRootPath: cfg.ArgoCDPathPrefix,
		Insecure:        cfg.ArgoCDInsecure,
		PlainText:       cfg.ArgoCDPlainText,
	}

	log.Info().
		Str("server-addr", opts.ServerAddr).
		Int("auth-token", len(opts.AuthToken)).
		Str("grpc-web-root-path", opts.GRPCWebRootPath).
		Bool("insecure", cfg.ArgoCDInsecure).
		Msg("ArgoCD client configuration")

	argo, err := apiclient.NewClient(opts)
	if err != nil {
		return nil, err
	}

	return &ArgoClient{
		cfg:       cfg,
		client:    argo,
		k8s:       k8s.ClientSet(),
		k8sConfig: k8s.Config(),
	}, nil
}

// ArgoDB returns a shared ArgoDB; its SettingsManager's informers live for the process.
func (a *ArgoClient) ArgoDB() db.ArgoDB {
	_, argoDB := a.argoSettings()
	return argoDB
}

func (a *ArgoClient) argoSettings() (*argosettings.SettingsManager, db.ArgoDB) {
	a.argoDBOnce.Do(func() {
		a.settingsMgr = argosettings.NewSettingsManager(context.Background(), a.k8s, a.cfg.ArgoCDNamespace)
		a.argoDB = db.NewDB(a.cfg.ArgoCDNamespace, a.settingsMgr, a.k8s)
	})
	return a.settingsMgr, a.argoDB
}

func (a *ArgoClient) createRepoServerClient() (repoapiclient.RepoServerServiceClient, *grpc.ClientConn, error) {
	tlsConfig := tls.Config{InsecureSkipVerify: a.cfg.ArgoCDRepositoryInsecure}
	conn, err := grpc.NewClient(a.cfg.ArgoCDRepositoryEndpoint,
		grpc.WithTransportCredentials(
			credentials.NewTLS(&tlsConfig),
		),
	)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create client")
	}

	return repoapiclient.NewRepoServerServiceClient(conn), conn, nil
}

var newClientBackOff = func() backoff.BackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = time.Second
	b.MaxInterval = 15 * time.Second
	b.MaxElapsedTime = 2 * time.Minute
	return b
}

func isTransientDialError(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func newClientWithRetry[T any](ctx context.Context, name string, newClient func() (io.Closer, T, error)) (io.Closer, T, error) {
	var (
		closer io.Closer
		c      T
	)
	err := backoff.RetryNotify(
		func() error {
			var err error
			closer, c, err = newClient()
			if err != nil && !isTransientDialError(err) {
				return backoff.Permanent(err)
			}
			return err
		},
		backoff.WithContext(newClientBackOff(), ctx),
		func(err error, wait time.Duration) {
			log.Warn().Err(err).Dur("retry_in", wait).Msgf("could not create ArgoCD %s, retrying", name)
		},
	)
	if err != nil {
		return nil, c, fmt.Errorf("could not create ArgoCD %s: %w", name, err)
	}
	return closer, c, nil
}

// GetApplicationClient has related argocd diff code https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L899
func (a *ArgoClient) GetApplicationClient(ctx context.Context) (io.Closer, application.ApplicationServiceClient, error) {
	return newClientWithRetry(ctx, "Application Client", a.client.NewApplicationClient)
}

func (a *ArgoClient) GetApplicationSetClient(ctx context.Context) (io.Closer, applicationset.ApplicationSetServiceClient, error) {
	return newClientWithRetry(ctx, "Application Set Client", a.client.NewApplicationSetClient)
}

func (a *ArgoClient) GetSettingsClient(ctx context.Context) (io.Closer, settings.SettingsServiceClient, error) {
	return newClientWithRetry(ctx, "Settings Client", a.client.NewSettingsClient)
}

func (a *ArgoClient) GetClusterClient(ctx context.Context) (io.Closer, cluster.ClusterServiceClient, error) {
	return newClientWithRetry(ctx, "Cluster Client", a.client.NewClusterClient)
}
