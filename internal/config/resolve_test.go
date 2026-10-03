package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hugoh/hrd/backends/git"
	"github.com/hugoh/hrd/internal/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	git.Register()
	os.Exit(m.Run())
}

func fakeGitRepo(t *testing.T, dir string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o750))
}

func TestResolveRoots_DiscoversRepos(t *testing.T) {
	backend.ResetDetectCache()

	root := t.TempDir()
	repoPath := filepath.Join(root, "foo")
	fakeGitRepo(t, repoPath)

	cfg := &Config{
		Repos: map[string]Repo{},
		Roots: map[string]Root{
			"myroot": {Path: root},
		},
	}

	errs := cfg.ResolveRoots()
	assert.Empty(t, errs)
	require.Contains(t, cfg.Repos, "foo")
	assert.Equal(t, repoPath, cfg.Repos["foo"].Path)
}

// rootWithOneFooRepo creates a temp root dir containing a single fake git
// repo, "foo", for tests exercising ResolveRoots.
func rootWithOneFooRepo(t *testing.T) string {
	t.Helper()
	backend.ResetDetectCache()

	root := t.TempDir()
	fakeGitRepo(t, filepath.Join(root, "foo"))

	return root
}

func TestResolveRoots_DoesNotPersistToRoots(t *testing.T) {
	root := rootWithOneFooRepo(t)

	cfg := &Config{
		Repos: map[string]Repo{},
		Roots: map[string]Root{
			"myroot": {Path: root},
		},
	}

	cfg.ResolveRoots()

	// Only the root itself is in Roots; the discovered repo isn't added there.
	assert.Len(t, cfg.Roots, 1)
}

func TestResolveRoots_StaticRepoWinsCollision(t *testing.T) {
	backend.ResetDetectCache()

	root := t.TempDir()
	repoPath := filepath.Join(root, "foo")
	fakeGitRepo(t, repoPath)

	staticPath := "/somewhere/else"
	cfg := &Config{
		Repos: map[string]Repo{
			"foo": {Path: staticPath},
		},
		Roots: map[string]Root{
			"myroot": {Path: root},
		},
	}

	errs := cfg.ResolveRoots()
	assert.Empty(t, errs)
	assert.Equal(t, staticPath, cfg.Repos["foo"].Path)
	require.Contains(t, cfg.Repos, "myroot-foo")
	assert.Equal(t, repoPath, cfg.Repos["myroot-foo"].Path)
}

func TestResolveRoots_CollisionFallsBackToRootName(t *testing.T) {
	backend.ResetDetectCache()

	rootA := t.TempDir()
	fakeGitRepo(t, filepath.Join(rootA, "foo"))

	rootB := t.TempDir()
	fooB := filepath.Join(rootB, "foo")
	fakeGitRepo(t, fooB)

	cfg := &Config{
		Repos: map[string]Repo{
			"foo": {Path: filepath.Join(rootA, "foo")},
		},
		Roots: map[string]Root{
			"rootb": {Path: rootB},
		},
	}

	errs := cfg.ResolveRoots()
	assert.Empty(t, errs)
	require.Contains(t, cfg.Repos, "rootb-foo")
	assert.Equal(t, fooB, cfg.Repos["rootb-foo"].Path)
}

func TestResolveRoots_ReusesPersistedFallback(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "foo")
	fakeGitRepo(t, path)

	cfg := Config{
		Repos: map[string]Repo{
			"foo":      {Path: "/static/foo"},
			"root-foo": {Path: path, Groups: []string{"focus", "owner"}},
		},
		Roots: map[string]Root{
			"root": {Path: root, Groups: []string{"owner", "team"}},
		},
	}

	require.Empty(t, cfg.ResolveRoots())
	require.Empty(t, cfg.ResolveRoots())
	assert.Equal(t, map[string]Repo{
		"foo":      {Path: "/static/foo"},
		"root-foo": {Path: path, Groups: []string{"focus", "owner", "team"}},
	}, cfg.Repos)
	assert.Equal(t, []string{"root-foo"}, cfg.Groups["owner"].Repos)
	assert.Equal(t, []string{"root-foo"}, cfg.Groups["team"].Repos)

	delete(cfg.Repos, "foo")
	require.Empty(t, cfg.ResolveRoots())
	assert.Len(t, cfg.Repos, 1, "the persisted name must survive removal of the original collision")
	assert.Equal(t, []string{"root-foo"}, cfg.Groups["owner"].Repos)
}

func TestResolveRoots_SkipsWhenFallbackAlsoTaken(t *testing.T) {
	backend.ResetDetectCache()

	rootB := t.TempDir()
	fooB := filepath.Join(rootB, "foo")
	fakeGitRepo(t, fooB)

	cfg := &Config{
		Repos: map[string]Repo{
			"foo":       {Path: "/static/foo"},
			"rootb-foo": {Path: "/static/rootb-foo"},
		},
		Roots: map[string]Root{
			"rootb": {Path: rootB},
		},
	}

	errs := cfg.ResolveRoots()
	assert.NotEmpty(t, errs)
	assert.Equal(t, "/static/foo", cfg.Repos["foo"].Path)
	assert.Equal(t, "/static/rootb-foo", cfg.Repos["rootb-foo"].Path)
}

func TestResolveRoots_InheritsGroupsFromRoot(t *testing.T) {
	root := rootWithOneFooRepo(t)

	cfg := &Config{
		Repos: map[string]Repo{},
		Roots: map[string]Root{
			"myroot": {Path: root, Groups: []string{"personal"}},
		},
	}

	errs := cfg.ResolveRoots()
	assert.Empty(t, errs)
	assert.Equal(t, []string{"personal"}, cfg.Repos["foo"].Groups)
	assert.Contains(t, cfg.Groups, "personal")
	assert.Equal(t, []string{"foo"}, cfg.Groups["personal"].Repos)
}

func TestResolveRoots_DefaultsDepthToOne(t *testing.T) {
	backend.ResetDetectCache()

	root := t.TempDir()
	// nested two levels deep, should NOT be found with default depth 1
	fakeGitRepo(t, filepath.Join(root, "a", "b"))

	cfg := &Config{
		Repos: map[string]Repo{},
		Roots: map[string]Root{
			"myroot": {Path: root},
		},
	}

	cfg.ResolveRoots()
	assert.Empty(t, cfg.Repos)
}

func TestResolveRoots_ExplicitDepth(t *testing.T) {
	backend.ResetDetectCache()

	root := t.TempDir()
	repoPath := filepath.Join(root, "a", "b")
	fakeGitRepo(t, repoPath)

	cfg := &Config{
		Repos: map[string]Repo{},
		Roots: map[string]Root{
			"myroot": {Path: root, Depth: 2},
		},
	}

	errs := cfg.ResolveRoots()
	assert.Empty(t, errs)
	require.Contains(t, cfg.Repos, "b")
	assert.Equal(t, repoPath, cfg.Repos["b"].Path)
}

func TestLoadResolved_MergesRoots(t *testing.T) {
	backend.ResetDetectCache()

	dir := t.TempDir()
	root := filepath.Join(dir, "roots")
	repoPath := filepath.Join(root, "foo")
	fakeGitRepo(t, repoPath)

	path := filepath.Join(dir, "config.toml")
	require.NoError(t, Save(path, Config{
		Repos: map[string]Repo{},
		Roots: map[string]Root{"myroot": {Path: root}},
	}))

	cfg, warnings, err := LoadResolved(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Contains(t, cfg.Repos, "foo")
	assert.Equal(t, repoPath, cfg.Repos["foo"].Path)
}

func TestLoadResolved_PropagatesLoadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("invalid toml {{"), 0o644))

	_, _, err := LoadResolved(path)
	require.Error(t, err)
}

func TestResolveRoots_NonexistentRootReportsError(t *testing.T) {
	backend.ResetDetectCache()

	cfg := &Config{
		Repos: map[string]Repo{},
		Roots: map[string]Root{
			"gone": {Path: filepath.Join(t.TempDir(), "does-not-exist")},
		},
	}

	errs := cfg.ResolveRoots()
	assert.NotEmpty(t, errs)
}
