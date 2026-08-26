package generator

import (
	"context"
	"encoding/json"
	"fmt"

	argogenerator "github.com/argoproj/argo-cd/v3/applicationset/generators"
	"github.com/argoproj/argo-cd/v3/applicationset/services"
	"github.com/argoproj/argo-cd/v3/applicationset/utils"
	"github.com/argoproj/argo-cd/v3/common"
	argov1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v3/util/db"
	argosettings "github.com/argoproj/argo-cd/v3/util/settings"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func New() AppsGenerator {
	return &gen{}
}

type gen struct {
}

// PRContext describes the PR whose state the AppSet should be rendered
// against. When non-empty, any Git generator on the AppSet whose RepoURL
// canonicalizes to RepoURL has its Revision overridden to HeadSHA so the
// generator sees files added/modified in the PR — including net-new files
// that would create net-new AppSet-generated Applications.
type PRContext struct {
	RepoURL string
	HeadSHA string
}

type AppsGenerator interface {
	GenerateApplicationSetApps(ctx context.Context, appset argov1alpha1.ApplicationSet, ctr *container.Container, pr PRContext) ([]argov1alpha1.Application, error)
}

func (c *gen) GenerateApplicationSetApps(ctx context.Context, appset argov1alpha1.ApplicationSet, ctr *container.Container, pr PRContext) ([]argov1alpha1.Application, error) {

	// Render against the PR HEAD instead of the AppSet's configured
	// revision so that diffs include AppSet-generated Apps from net-new
	// files in the PR.
	if pr.HeadSHA != "" && pr.RepoURL != "" {
		appset = overrideGitRevisionForPR(appset, pr.RepoURL, pr.HeadSHA)
	}

	repos := newRepoService(ctx, ctr)
	appSetGenerators := getGenerators(*ctr.KubeClientSet.ControllerClient(), ctr.KubeClientSet.ClientSet(), ctr.Config.ArgoCDNamespace, repos)

	apps, appsetReason, err := generateApplications(appset, appSetGenerators, *ctr.KubeClientSet.ControllerClient())
	if err != nil {
		fmt.Printf("error generating applications: %v, appset reason: %v", err, appsetReason)
		return nil, fmt.Errorf("error generating applications: %w", err)
	}
	return apps, nil
}

// overrideGitRevisionForPR returns a copy of appset with every Git
// generator that targets prRepoURL pointed at prHeadSHA. It also stamps the
// refresh annotation so argo's Git generator bypasses the revision cache
// (otherwise argocd-repo-server would happily serve a cached ls-tree for
// the SHA — fine — but the cache lifetime can mask brand-new SHAs during
// race conditions).
func overrideGitRevisionForPR(appset argov1alpha1.ApplicationSet, prRepoURL, prHeadSHA string) argov1alpha1.ApplicationSet {
	out := *appset.DeepCopy()
	matched := false
	for i := range out.Spec.Generators {
		gen := &out.Spec.Generators[i]
		if pointGitToPR(gen.Git, prRepoURL, prHeadSHA) {
			matched = true
		}
		if gen.Matrix != nil {
			for j := range gen.Matrix.Generators {
				if pointGitToPR(gen.Matrix.Generators[j].Git, prRepoURL, prHeadSHA) {
					matched = true
				}
			}
		}
		if gen.Merge != nil {
			for j := range gen.Merge.Generators {
				if pointGitToPR(gen.Merge.Generators[j].Git, prRepoURL, prHeadSHA) {
					matched = true
				}
			}
		}
	}
	if matched {
		if out.Annotations == nil {
			out.Annotations = map[string]string{}
		}
		out.Annotations[common.AnnotationApplicationSetRefresh] = "true"
		log.Debug().
			Str("appset", out.Name).
			Str("revision", prHeadSHA).
			Msg("overriding Git generator revision to PR HEAD")
	}
	return out
}

// pointGitToPR redirects a Git generator at prHeadSHA when its RepoURL
// matches prRepoURL. Returns true if the generator was modified.
func pointGitToPR(g *argov1alpha1.GitGenerator, prRepoURL, prHeadSHA string) bool {
	if g == nil || g.RepoURL == "" {
		return false
	}
	if !pkg.AreSameRepos(g.RepoURL, prRepoURL) {
		return false
	}
	g.Revision = prHeadSHA
	return true
}

// GetGenerators returns the generators that will be used to generate applications for the ApplicationSet
//
// supports List, Clusters, and Git generators (plus Matrix/Merge composition of those)
func getGenerators(c client.Client, k8sClient kubernetes.Interface, namespace string, repos services.Repos) map[string]argogenerator.Generator {

	terminalGenerators := map[string]argogenerator.Generator{
		"List":     argogenerator.NewListGenerator(),
		"Clusters": argogenerator.NewClusterGenerator(c, namespace),
		"Git":      argogenerator.NewGitGenerator(repos, namespace),
	}

	nestedGenerators := map[string]argogenerator.Generator{
		"List":     terminalGenerators["List"],
		"Clusters": terminalGenerators["Clusters"],
		"Git":      terminalGenerators["Git"],
		"Matrix":   argogenerator.NewMatrixGenerator(terminalGenerators),
		"Merge":    argogenerator.NewMergeGenerator(terminalGenerators),
	}

	topLevelGenerators := map[string]argogenerator.Generator{
		"List":     terminalGenerators["List"],
		"Clusters": terminalGenerators["Clusters"],
		"Git":      terminalGenerators["Git"],
		"Matrix":   argogenerator.NewMatrixGenerator(nestedGenerators),
		"Merge":    argogenerator.NewMergeGenerator(nestedGenerators),
	}
	return topLevelGenerators
}

// newRepoService builds the services.Repos used by the Git generator. It talks
// to argocd-repo-server over gRPC (kubechecks already does this for manifest
// generation) and reads repository credentials from argocd Secrets/ConfigMaps
// via the SettingsManager.
func newRepoService(ctx context.Context, ctr *container.Container) services.Repos {
	k8s := ctr.KubeClientSet.ClientSet()
	ns := ctr.Config.ArgoCDNamespace
	settingsMgr := argosettings.NewSettingsManager(ctx, k8s, ns)
	argoDB := db.NewDB(ns, settingsMgr, k8s)
	repoClientset := apiclient.NewRepoServerClientset(
		ctr.Config.ArgoCDRepositoryEndpoint,
		0,
		apiclient.TLSConfiguration{
			DisableTLS:       false,
			StrictValidation: !ctr.Config.ArgoCDRepositoryInsecure,
		},
	)
	return services.NewArgoCDService(argoDB, false, repoClientset, true)
}

// generateApplications generates applications from the ApplicationSet
func generateApplications(applicationSetInfo argov1alpha1.ApplicationSet, g map[string]argogenerator.Generator, client client.Client) (
	[]argov1alpha1.Application, argov1alpha1.ApplicationSetReasonType, error,
) {
	var res []argov1alpha1.Application
	renderer := &utils.Render{}
	var firstError error
	var applicationSetReason argov1alpha1.ApplicationSetReasonType

	for _, requestedGenerator := range applicationSetInfo.Spec.Generators {
		t, err := argogenerator.Transform(requestedGenerator, g, applicationSetInfo.Spec.Template, &applicationSetInfo, map[string]interface{}{}, client)
		if err != nil {
			if firstError == nil {
				firstError = err
				applicationSetReason = argov1alpha1.ApplicationSetReasonApplicationParamsGenerationError
			}
			continue
		}

		for _, a := range t {
			tmplApplication := getTempApplication(a.Template)

			for _, p := range a.Params {
				app, err := renderer.RenderTemplateParams(tmplApplication, applicationSetInfo.Spec.SyncPolicy, p, applicationSetInfo.Spec.GoTemplate, applicationSetInfo.Spec.GoTemplateOptions)
				if err != nil {
					//logCtx.WithError(err).WithField("params", a.Params).WithField("generator", requestedGenerator).
					//	Error("error generating application from params")

					if firstError == nil {
						firstError = err
						applicationSetReason = argov1alpha1.ApplicationSetReasonRenderTemplateParamsError
					}
					continue
				}

				if applicationSetInfo.Spec.TemplatePatch != nil {
					patchedApplication, err := renderTemplatePatch(renderer, app, applicationSetInfo, p)
					if err != nil {
						if firstError == nil {
							firstError = err
							applicationSetReason = argov1alpha1.ApplicationSetReasonRenderTemplateParamsError
						}
						continue
					}

					app = patchedApplication
				}

				// The app's namespace must be the same as the AppSet's namespace to preserve the appsets-in-any-namespace
				// security boundary.
				app.Namespace = applicationSetInfo.Namespace
				res = append(res, *app)
			}
		}

		//logCtx.WithField("generator", requestedGenerator).Infof("generated %d applications", len(res))
		//logCtx.WithField("generator", requestedGenerator).Debugf("apps from generator: %+v", res)
	}

	return res, applicationSetReason, firstError
}

func renderTemplatePatch(r utils.Renderer, app *argov1alpha1.Application, applicationSetInfo argov1alpha1.ApplicationSet, params map[string]interface{}) (*argov1alpha1.Application, error) {
	replacedTemplate, err := r.Replace(*applicationSetInfo.Spec.TemplatePatch, params, applicationSetInfo.Spec.GoTemplate, applicationSetInfo.Spec.GoTemplateOptions)
	if err != nil {
		return nil, fmt.Errorf("error replacing values in templatePatch: %w", err)
	}

	return applyTemplatePatch(app, replacedTemplate)
}

func getTempApplication(applicationSetTemplate argov1alpha1.ApplicationSetTemplate) *argov1alpha1.Application {
	tmplApplication := argov1alpha1.Application{}
	tmplApplication.Annotations = applicationSetTemplate.Annotations
	tmplApplication.Labels = applicationSetTemplate.Labels
	tmplApplication.Namespace = applicationSetTemplate.Namespace
	tmplApplication.Name = applicationSetTemplate.Name
	tmplApplication.Spec = applicationSetTemplate.Spec
	tmplApplication.Finalizers = applicationSetTemplate.Finalizers
	tmplApplication.APIVersion = "argoproj.io/v1alpha1"
	tmplApplication.Kind = "Application"
	return &tmplApplication
}

func applyTemplatePatch(app *argov1alpha1.Application, templatePatch string) (*argov1alpha1.Application, error) {

	appString, err := json.Marshal(app)
	if err != nil {
		return nil, fmt.Errorf("error while marhsalling Application %w", err)
	}

	convertedTemplatePatch, err := utils.ConvertYAMLToJSON(templatePatch)

	if err != nil {
		return nil, fmt.Errorf("error while converting template to json %q: %w", convertedTemplatePatch, err)
	}

	if err := json.Unmarshal([]byte(convertedTemplatePatch), &argov1alpha1.Application{}); err != nil {
		return nil, fmt.Errorf("invalid templatePatch %q: %w", convertedTemplatePatch, err)
	}

	data, err := strategicpatch.StrategicMergePatch(appString, []byte(convertedTemplatePatch), argov1alpha1.Application{})

	if err != nil {
		return nil, fmt.Errorf("error while applying templatePatch template to json %q: %w", convertedTemplatePatch, err)
	}

	finalApp := argov1alpha1.Application{}
	err = json.Unmarshal(data, &finalApp)
	if err != nil {
		return nil, fmt.Errorf("error while unmarhsalling patched application: %w", err)
	}

	// Prevent changes to the `project` field. This helps prevent malicious template patches
	finalApp.Spec.Project = app.Spec.Project

	return &finalApp, nil
}
