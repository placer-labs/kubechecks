package appdir

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/git"
	"sigs.k8s.io/yaml"
)

// AppSetDirectory manages the mapping between ApplicationSets and their associated directories and files.
// It provides functionality to track which ApplicationSets are affected by changes in specific directories or files,
// and maintains a collection of Argo CD ApplicationSets.
type AppSetDirectory struct {
	// appSetDirs maps directory paths to the names of ApplicationSets that use those directories.
	// This is used to quickly identify which ApplicationSets are affected when changes occur in a directory.
	appSetDirs map[string][]string

	// appSetFiles maps file paths to the names of ApplicationSets that use those files.
	// This is used to quickly identify which ApplicationSets are affected when specific files change.
	appSetFiles map[string][]string

	// appSetsMap stores the full Argo CD ApplicationSet definitions, indexed by ApplicationSet name.
	// This serves as the source of truth for ApplicationSet configurations.
	appSetsMap map[string]v1alpha1.ApplicationSet

	// appSetDirPatterns maps a directory glob (from a Git directory generator)
	// to the names of ApplicationSets that consume directories matching that
	// glob. A changed file under any matching directory triggers the AppSet.
	appSetDirPatterns map[string][]string

	// appSetFilePatterns maps a file glob (from a Git file generator) to the
	// names of ApplicationSets that consume files matching that glob.
	appSetFilePatterns map[string][]string
}

func NewAppSetDirectory() *AppSetDirectory {
	return &AppSetDirectory{
		appSetDirs:         make(map[string][]string),
		appSetFiles:        make(map[string][]string),
		appSetsMap:         make(map[string]v1alpha1.ApplicationSet),
		appSetDirPatterns:  make(map[string][]string),
		appSetFilePatterns: make(map[string][]string),
	}
}

func (d *AppSetDirectory) Count() int {
	return len(d.appSetsMap)
}

func (d *AppSetDirectory) Union(other *AppSetDirectory) *AppSetDirectory {
	var join AppSetDirectory
	join.appSetsMap = mergeMaps(d.appSetsMap, other.appSetsMap, takeFirst[v1alpha1.ApplicationSet])
	join.appSetDirs = mergeMaps(d.appSetDirs, other.appSetDirs, mergeLists[string])
	join.appSetFiles = mergeMaps(d.appSetFiles, other.appSetFiles, mergeLists[string])
	join.appSetDirPatterns = mergeMaps(d.appSetDirPatterns, other.appSetDirPatterns, mergeLists[string])
	join.appSetFilePatterns = mergeMaps(d.appSetFilePatterns, other.appSetFilePatterns, mergeLists[string])
	return &join
}

func (d *AppSetDirectory) ProcessAppSet(app v1alpha1.ApplicationSet) {
	appName := app.GetName()

	src := app.Spec.Template.Spec.GetSource()

	// common data
	srcPath := src.Path
	d.AddAppSet(&app)

	// handle extra helm paths
	if helm := src.Helm; helm != nil {
		for _, param := range helm.FileParameters {
			path := filepath.Join(srcPath, param.Path)
			d.AddFile(appName, path)
		}

		for _, valueFilePath := range helm.ValueFiles {
			path := filepath.Join(srcPath, valueFilePath)
			d.AddFile(appName, path)
		}
	}

	// Register Git generator path patterns so that changes under those
	// directories/files trigger this AppSet — without this, AppSets that use
	// Git directory/file generators are never matched against a PR's
	// changed-file list and produce no diff. Handles top-level Git generators
	// plus one level of Matrix/Merge nesting (the common case).
	for i := range app.Spec.Generators {
		gen := &app.Spec.Generators[i]
		d.registerGitGen(appName, gen.Git)
		if gen.Matrix != nil {
			for j := range gen.Matrix.Generators {
				d.registerGitGen(appName, gen.Matrix.Generators[j].Git)
			}
		}
		if gen.Merge != nil {
			for j := range gen.Merge.Generators {
				d.registerGitGen(appName, gen.Merge.Generators[j].Git)
			}
		}
	}
}

// registerGitGen records the directory/file globs from a Git generator
// against the AppSet name. Excluded entries and entries with empty paths are
// skipped.
func (d *AppSetDirectory) registerGitGen(appName string, g *v1alpha1.GitGenerator) {
	if g == nil {
		return
	}
	for _, dir := range g.Directories {
		if dir.Exclude || dir.Path == "" {
			continue
		}
		d.appSetDirPatterns[dir.Path] = append(d.appSetDirPatterns[dir.Path], appName)
	}
	for _, f := range g.Files {
		if f.Exclude || f.Path == "" {
			continue
		}
		d.appSetFilePatterns[f.Path] = append(d.appSetFilePatterns[f.Path], appName)
	}
}

// FindAppSetsBasedOnChangeList receives the modified file path and
// returns the list of applications that are affected by the changes.
//
//	e.g. changeList = ["/appset/httpdump/httpdump.yaml", "/app/testapp/values.yaml"]
//  if the changed file is application set file, return it.

func (d *AppSetDirectory) FindAppSetsBasedOnChangeList(changeList []string, repo *git.Repo) []v1alpha1.ApplicationSet {
	log.Debug().Caller().
		Str("type", "applicationsets").
		Msgf("checking %d changes", len(changeList))

	appsSet := make(map[string]struct{})
	var appSets []v1alpha1.ApplicationSet

	for _, changePath := range changeList {
		log.Debug().Caller().
			Msgf("changePath: %s", changePath)
		absPath := filepath.Join(repo.Directory, changePath)

		// Check if file contains `kind: ApplicationSet`
		if !containsKindApplicationSet(absPath) {
			continue
		}

		// Open the yaml file and parse it as v1alpha1.ApplicationSet
		fileContent, err := os.ReadFile(absPath)
		if err != nil {
			log.Error().Msgf("failed to open file %s: %v", absPath, err)
			continue
		}

		appSet := &v1alpha1.ApplicationSet{}
		err = yaml.Unmarshal(fileContent, appSet)
		if err != nil {
			log.Error().Msgf("failed to parse file %s as ApplicationSet: %v", absPath, err)
			continue
		}

		// Store the unique ApplicationSet
		if _, exists := appsSet[appSet.Name]; !exists {
			appsSet[appSet.Name] = struct{}{}
			appSets = append(appSets, *appSet)
		}
	}

	// Second pass: match changed files against Git generator path globs from
	// AppSets that were discovered via the cluster API (not via files in the
	// PR). This is what catches PRs that only touch values/variants files,
	// not the AppSet manifest itself.
	for _, changePath := range changeList {
		// Normalize to forward-slash relative path for glob matching.
		cleanPath := path.Clean(strings.TrimPrefix(changePath, "/"))

		// Directory generators: walk up parents of the changed file and
		// match each parent against the registered globs. If any parent
		// matches, the change is inside a directory that the AppSet's
		// generator would have produced.
		for pattern, appNames := range d.appSetDirPatterns {
			if !anyParentMatches(pattern, cleanPath) {
				continue
			}
			for _, name := range appNames {
				if _, exists := appsSet[name]; exists {
					continue
				}
				appSet, ok := d.appSetsMap[name]
				if !ok {
					continue
				}
				appsSet[name] = struct{}{}
				appSets = append(appSets, appSet)
			}
		}

		// File generators: match the changed file path directly against the
		// glob.
		for pattern, appNames := range d.appSetFilePatterns {
			ok, err := path.Match(pattern, cleanPath)
			if err != nil || !ok {
				continue
			}
			for _, name := range appNames {
				if _, exists := appsSet[name]; exists {
					continue
				}
				appSet, ok := d.appSetsMap[name]
				if !ok {
					continue
				}
				appsSet[name] = struct{}{}
				appSets = append(appSets, appSet)
			}
		}
	}

	log.Debug().Caller().Str("source", "appset_directory").Msgf("matched %d files into %d appset", len(changeList), len(appSets))
	return appSets
}

// anyParentMatches reports whether any directory along the parent chain of
// filePath matches the glob pattern. Used to decide whether a changed file
// lives inside a directory enumerated by a Git directory generator.
func anyParentMatches(pattern, filePath string) bool {
	dir := path.Dir(filePath)
	for {
		if ok, err := path.Match(pattern, dir); err == nil && ok {
			return true
		}
		if dir == "." || dir == "/" {
			return false
		}
		parent := path.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func appSetGetSourcePath(app *v1alpha1.ApplicationSet) string {
	return app.Spec.Template.Spec.GetSource().Path
}

func (d *AppSetDirectory) GetAppSets(filter func(stub v1alpha1.ApplicationSet) bool) []v1alpha1.ApplicationSet {
	var result []v1alpha1.ApplicationSet
	for _, value := range d.appSetsMap {
		if filter != nil && !filter(value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (d *AppSetDirectory) AddAppSet(appSet *v1alpha1.ApplicationSet) {
	if _, exists := d.appSetsMap[appSet.GetName()]; exists {
		log.Info().Msgf("appset %s already exists", appSet.Name)
		return
	}
	log.Debug().
		Caller().
		Str("appName", appSet.GetName()).
		Str("source", appSetGetSourcePath(appSet)).
		Msg("add appset")
	d.appSetsMap[appSet.GetName()] = *appSet
	d.AddDir(appSet.GetName(), appSetGetSourcePath(appSet))
}

func (d *AppSetDirectory) AddDir(appName, path string) {
	d.appSetDirs[path] = append(d.appSetDirs[path], appName)
}

func (d *AppSetDirectory) AddFile(appName, path string) {
	d.appSetFiles[path] = append(d.appSetFiles[path], appName)
}

func (d *AppSetDirectory) RemoveAppSet(app v1alpha1.ApplicationSet) {
	log.Debug().
		Caller().
		Str("appName", app.Name).
		Msg("delete app")

	// remove app from appSetsMap
	delete(d.appSetsMap, app.Name)

	// Clean up app from appSetDirs
	sourcePath := appSetGetSourcePath(&app)
	d.appSetDirs[sourcePath] = removeFromSlice[string](d.appSetDirs[sourcePath], app.Name, func(a, b string) bool { return a == b })

	// Clean up app from appSetFiles
	src := app.Spec.Template.Spec.GetSource()
	srcPath := src.Path
	if helm := src.Helm; helm != nil {
		for _, param := range helm.FileParameters {
			path := filepath.Join(srcPath, param.Path)
			d.appSetFiles[path] = removeFromSlice[string](d.appSetFiles[path], app.Name, func(a, b string) bool { return a == b })
		}

		for _, valueFilePath := range helm.ValueFiles {
			path := filepath.Join(srcPath, valueFilePath)
			d.appSetFiles[path] = removeFromSlice[string](d.appSetFiles[path], app.Name, func(a, b string) bool { return a == b })
		}
	}

	// Clean up Git generator patterns
	for i := range app.Spec.Generators {
		gen := &app.Spec.Generators[i]
		d.unregisterGitGen(app.Name, gen.Git)
		if gen.Matrix != nil {
			for j := range gen.Matrix.Generators {
				d.unregisterGitGen(app.Name, gen.Matrix.Generators[j].Git)
			}
		}
		if gen.Merge != nil {
			for j := range gen.Merge.Generators {
				d.unregisterGitGen(app.Name, gen.Merge.Generators[j].Git)
			}
		}
	}
}

func (d *AppSetDirectory) unregisterGitGen(appName string, g *v1alpha1.GitGenerator) {
	if g == nil {
		return
	}
	eq := func(a, b string) bool { return a == b }
	for _, dir := range g.Directories {
		if dir.Path == "" {
			continue
		}
		d.appSetDirPatterns[dir.Path] = removeFromSlice[string](d.appSetDirPatterns[dir.Path], appName, eq)
	}
	for _, f := range g.Files {
		if f.Path == "" {
			continue
		}
		d.appSetFilePatterns[f.Path] = removeFromSlice[string](d.appSetFilePatterns[f.Path], appName, eq)
	}
}

// containsKindApplicationSet checks if the file contains kind: ApplicationSet
func containsKindApplicationSet(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		log.Error().Err(err).Stack().Msgf("failed to open file %s: %v", path, err)
		return false
	}
	defer pkg.WithErrorLogging(file.Close, "failed to close file")

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "kind: ApplicationSet") {
			log.Debug().Caller().Msgf("found kind: ApplicationSet in %s", path)
			return true
		}
	}

	if err := scanner.Err(); err != nil {
		log.Error().Err(err).Stack().Msgf("error reading file %s: %v", path, err)
	}

	return false
}
