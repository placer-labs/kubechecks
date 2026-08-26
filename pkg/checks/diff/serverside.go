package diff

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/argoproj/argo-cd/gitops-engine/pkg/diff"
	"github.com/argoproj/argo-cd/gitops-engine/pkg/sync/hook"
	"github.com/argoproj/argo-cd/gitops-engine/pkg/utils/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
)

// serverSideDiff asks the ArgoCD API to diff the PR's manifests against live
// state using a server-side apply dry run. The API server owns the destination
// cluster credentials, the OpenAI schema and the dry runs, so kubechecks needs
// no cluster access of its own -- only Get on the application.
//
// The win over the local diff is normalization: fields populated by API server
// defaulting or by mutating webhooks come back already reconciled, so resources
// nobody touched stop appearing as modified.
//
// Results are keyed by resource so callers can fall back per item. A nil map
// with a nil error means the caller should use the local diff.
func serverSideDiff(ctx context.Context, request checks.Request, items []objKeyLiveTarget, resources []*argoappv1.ResourceDiff) (map[kube.ResourceKey]diff.DiffResult, error) {
	ctx, span := tracer.Start(ctx, "serverSideDiff")
	defer span.End()

	cfg := request.Container.Config

	liveResources := make([]*argoappv1.ResourceDiff, 0, len(items))
	targetManifests := make([]string, 0, len(items))
	keys := make([]kube.ResourceKey, 0, len(items))

	for _, item := range items {
		if item.target != nil && hook.IsHook(item.target) || item.live != nil && hook.IsHook(item.live) {
			continue
		}

		live := findResourceDiff(resources, item.key)
		if live == nil {
			// Resource does not exist yet; the API needs a placeholder so the
			// live and target slices stay index-aligned.
			live = &argoappv1.ResourceDiff{
				Group:     item.key.Group,
				Kind:      item.key.Kind,
				Namespace: item.key.Namespace,
				Name:      item.key.Name,
				Modified:  true,
			}
		}

		var targetManifest string
		if item.target != nil {
			raw, err := json.Marshal(item.target)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal target for %s: %w", item.key, err)
			}
			targetManifest = string(raw)
		}

		liveResources = append(liveResources, live)
		targetManifests = append(targetManifests, targetManifest)
		keys = append(keys, item.key)
	}

	if len(liveResources) == 0 {
		return nil, nil
	}

	closer, appClient := request.Container.ArgoClient.GetApplicationClient()
	defer pkg.WithErrorLogging(closer.Close, "failed to close app client")

	app := request.App
	appName := app.Name
	appNamespace := app.Namespace
	project := app.Spec.Project

	batches := batchBySize(liveResources, targetManifests, int(cfg.ArgoCDServerSideDiffMaxBatchKB)*1024)
	results := make([][]*argoappv1.ResourceDiff, len(batches))

	g, gctx := errgroup.WithContext(ctx)
	if cfg.ArgoCDServerSideDiffConcurrency > 0 {
		g.SetLimit(int(cfg.ArgoCDServerSideDiffConcurrency))
	}

	for i := range batches {
		idx, b := i, batches[i]
		g.Go(func() error {
			res, err := appClient.ServerSideDiff(gctx, &application.ApplicationServerSideDiffQuery{
				AppName:         &appName,
				AppNamespace:    &appNamespace,
				Project:         &project,
				LiveResources:   liveResources[b.start:b.end],
				TargetManifests: targetManifests[b.start:b.end],
			})
			if err != nil {
				return err
			}
			results[idx] = res.Items
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	out := make(map[kube.ResourceKey]diff.DiffResult, len(keys))
	for _, batch := range results {
		for _, item := range batch {
			key := kube.ResourceKey{
				Group:     item.Group,
				Kind:      item.Kind,
				Namespace: item.Namespace,
				Name:      item.Name,
			}
			out[key] = diff.DiffResult{
				Modified: item.Modified,
				// TargetState is the predicted result of applying the manifest,
				// which is what the renderer expects in PredictedLive.
				PredictedLive:  jsonOrNil(item.TargetState),
				NormalizedLive: jsonOrNil(item.LiveState),
			}
		}
	}

	serverSideDiffSuccess.WithLabelValues(appName).Inc()

	// Info rather than Debug: without this there is no way to tell a successful
	// server-side diff from a silent fallback at the default log level.
	log.Info().Caller().
		Str("app", appName).
		Int("resources", len(keys)).
		Int("batches", len(batches)).
		Int("results", len(out)).
		Msg("server-side diff complete")

	return out, nil
}

type sizeBatch struct{ start, end int }

// batchBySize groups resources so no single request exceeds maxBytes, which
// keeps individual payloads under whatever proxy sits in front of the API.
func batchBySize(liveResources []*argoappv1.ResourceDiff, targetManifests []string, maxBytes int) []sizeBatch {
	var batches []sizeBatch
	for i := 0; i < len(liveResources); {
		start, size := i, 0
		for i < len(liveResources) {
			resourceSize := len(liveResources[i].LiveState) + len(targetManifests[i])
			if size+resourceSize > maxBytes && i > start {
				break
			}
			size += resourceSize
			i++
		}
		batches = append(batches, sizeBatch{start: start, end: i})
	}
	return batches
}

func findResourceDiff(resources []*argoappv1.ResourceDiff, key kube.ResourceKey) *argoappv1.ResourceDiff {
	for _, res := range resources {
		if res.Group == key.Group && res.Kind == key.Kind &&
			res.Namespace == key.Namespace && res.Name == key.Name {
			return res
		}
	}
	return nil
}

func jsonOrNil(state string) []byte {
	if state == "" || state == "null" {
		return nil
	}
	return []byte(state)
}
