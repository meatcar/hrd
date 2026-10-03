package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hugoh/hrd/internal/config"
	"github.com/hugoh/hrd/internal/discover/discovertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zenizh/go-capturer"
)

func groupListConfig() config.Config {
	return config.Config{
		Repos: map[string]config.Repo{
			"repo1": {Path: "/tmp/repo1", Groups: []string{"work"}},
			"repo2": {Path: "/tmp/repo2", Groups: []string{"work"}},
			"repo3": {Path: "/tmp/repo3"},
		},
	}
}

func TestGroupList(t *testing.T) { //nolint:funlen
	tests := []struct {
		name        string
		cfg         config.Config
		args        []string
		wantErr     error
		wantOutput  string
		wantContent string
		wantErrText string
	}{
		{
			name:        "TestGroupListWithPanel",
			cfg:         groupListConfig(),
			args:        []string{"group", "ls"},
			wantContent: "work",
		},
		{
			name:        "TestGroupListNoGroups",
			cfg:         config.Config{Repos: map[string]config.Repo{}},
			args:        []string{"group", "ls"},
			wantContent: "no groups defined",
		},
		{
			name:        "TestGroupListShowsReservedGroups",
			cfg:         groupListConfig(),
			args:        []string{"group", "ls"},
			wantContent: "@@none",
		},
		{
			// Without --live, @@attention shows a hint instead of computing
			// live status, keeping the default listing a fast config-only read.
			name:        "TestGroupListShowsAttentionHintWithoutLive",
			cfg:         groupListConfig(),
			args:        []string{"group", "ls"},
			wantContent: "@@attention  (pass --live to compute)",
		},
		{
			name:        "TestGroupListNoGroupsStillShowsReservedGroups",
			cfg:         config.Config{Repos: map[string]config.Repo{}},
			args:        []string{"group", "ls"},
			wantContent: "@@none",
		},
		{
			// Every repo is grouped, so @@none's membership is empty and
			// must be shown explicitly, not as a blank/missing line.
			name: "TestGroupListReservedNoneShowsNoneWhenEmpty",
			cfg: config.Config{
				Repos: map[string]config.Repo{
					"repo1": {Path: "/tmp/repo1", Groups: []string{"work"}},
				},
			},
			args:        []string{"group", "ls"},
			wantContent: "@@none\n  (none)",
		},
		{
			name:       "TestGroupListWithName",
			cfg:        groupListConfig(),
			args:       []string{"group", "ls", "work"},
			wantOutput: "repo1\nrepo2\n",
		},
		{
			name:       "TestGroupListWithNameAtPrefix",
			cfg:        groupListConfig(),
			args:       []string{"group", "ls", "@work"},
			wantOutput: "repo1\nrepo2\n",
		},
		{
			name: "TestGroupListUnknownName",
			cfg: config.Config{
				Repos: map[string]config.Repo{
					"repo1": {Path: "/tmp/repo1", Groups: []string{"work"}},
				},
			},
			args:    []string{"group", "ls", "nonexistent"},
			wantErr: errUnknownGroup,
		},
		{
			name:       "TestGroupListReservedNone",
			cfg:        groupListConfig(),
			args:       []string{"group", "ls", "@@none"},
			wantOutput: "repo3\n",
		},
		{
			name: "TestGroupListUnknownAtName",
			cfg: config.Config{
				Repos: map[string]config.Repo{
					"repo1": {Path: "/tmp/repo1", Groups: []string{"work"}},
				},
			},
			args:        []string{"group", "ls", "@nonexistent"},
			wantErr:     errUnknownGroup,
			wantErrText: "@nonexistent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgPath := setupTestConfig(t, tt.cfg)

			app := newTestApp()

			var err error

			stdout := capturer.CaptureStdout(func() {
				fullArgs := append([]string{"hrd", "--config", cfgPath}, tt.args...)
				err = RunApp(t.Context(), app, fullArgs)
			})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				if tt.wantErrText != "" {
					assert.Contains(t, err.Error(), tt.wantErrText)
				}

				return
			}

			require.NoError(t, err)

			if tt.wantOutput != "" {
				assert.Equal(t, tt.wantOutput, stdout)
			}

			if tt.wantContent != "" {
				assert.Contains(t, stdout, tt.wantContent)
			}
		})
	}
}

// twoRepoConfig writes a config with two ungrouped repos, for tests
// exercising group membership commands.
func twoRepoConfig(t *testing.T) string {
	t.Helper()

	return setupTestConfig(t, config.Config{
		Repos: map[string]config.Repo{
			"repo1": {Path: "/tmp/repo1"},
			"repo2": {Path: "/tmp/repo2"},
		},
	})
}

func TestGroupAdd(t *testing.T) {
	cfgPath := twoRepoConfig(t)

	err := runHRD(t, cfgPath, []string{"group", "add", "work", "repo1"})
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"work"}, cfg.Repos["repo1"].Groups)
	assert.Contains(t, cfg.Groups, "work")
	assert.Equal(t, []string{"repo1"}, cfg.Groups["work"].Repos)

	err = runHRD(t, cfgPath, []string{"group", "add", "work", "repo1"})
	require.NoError(t, err)

	cfg, err = config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"work"}, cfg.Repos["repo1"].Groups)
}

func TestGroupAddDiscoveredRepo(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		discovertest.FakeGitDirAt(t, filepath.Join(root, name))
	}

	t.Chdir(filepath.Join(root, "beta"))

	for _, tt := range []struct{ name, arg string }{
		{"name", "beta"},
		{"path", filepath.Join(root, "beta")},
		{"dot", "."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			roots := map[string]config.Root{"owner": {Path: root, Groups: []string{"owner"}}}
			cfgPath := setupTestConfig(t, config.Config{
				Repos: map[string]config.Repo{
					"alpha": {Path: filepath.Join(root, "alpha"), Groups: []string{"existing"}},
				},
				Roots: roots,
			})

			for range 2 {
				err := runHRD(t, cfgPath, []string{"group", "add", "@focus", "alpha", tt.arg})
				require.NoError(t, err)
			}

			saved, err := config.Load(cfgPath)
			require.NoError(t, err)
			assert.Equal(t, map[string]config.Repo{
				"alpha": {
					Path:   filepath.Join(root, "alpha"),
					Groups: []string{"existing", "focus"},
				},
				"beta": {Path: filepath.Join(root, "beta"), Groups: []string{"focus"}},
			}, saved.Repos)
			assert.Equal(t, roots, saved.Roots)

			resolved, warnings, err := config.LoadResolved(cfgPath)
			require.NoError(t, err)
			require.Empty(t, warnings)
			assert.Equal(t, []string{"alpha", "beta"}, resolved.Groups["focus"].Repos)
			assert.Equal(t, []string{"alpha", "beta", "gamma"}, resolved.Groups["owner"].Repos)
		})
	}
}

// TestGroupAddMultipleRepos verifies the group-first argument order allows
// adding several repos to one group in a single invocation.
func TestGroupAddMultipleRepos(t *testing.T) {
	cfgPath := twoRepoConfig(t)

	err := runHRD(t, cfgPath, []string{"group", "add", "work", "repo1", "repo2"})
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"work"}, cfg.Repos["repo1"].Groups)
	assert.Equal(t, []string{"work"}, cfg.Repos["repo2"].Groups)
	assert.ElementsMatch(t, []string{"repo1", "repo2"}, cfg.Groups["work"].Repos)
}

func TestGroupAddUnknownRepo(t *testing.T) {
	root := t.TempDir()
	discovertest.FakeGitDirAt(t, filepath.Join(root, "beta"))
	cfgPath := setupTestConfig(t, config.Config{
		Repos: map[string]config.Repo{"alpha": {Path: "/tmp/alpha"}},
		Roots: map[string]config.Root{"owner": {Path: root}},
	})
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	err = runHRD(t, cfgPath, []string{"group", "add", "work", "alpha", "beta", "nonexistent"})
	require.ErrorIs(t, err, errUnknownRepo)
	assert.Contains(t, err.Error(), "nonexistent")

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestGroupAddResolvesDirectoryArg(t *testing.T) {
	repoDir := t.TempDir()
	cfgPath := setupTestConfig(t, config.Config{
		Repos: map[string]config.Repo{
			"repo1": {Path: repoDir},
		},
	})

	err := runHRD(t, cfgPath, []string{"group", "add", "work", repoDir})
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"work"}, cfg.Repos["repo1"].Groups)
}

func TestGroupAddResolvesDotArg(t *testing.T) {
	repoDir := t.TempDir()
	cfgPath := setupTestConfig(t, config.Config{
		Repos: map[string]config.Repo{
			"repo1": {Path: repoDir},
		},
	})

	t.Chdir(repoDir)

	err := runHRD(t, cfgPath, []string{"group", "add", "work", "."})
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"work"}, cfg.Repos["repo1"].Groups)
}

func TestGroupRm(t *testing.T) {
	cfgPath := setupTestConfig(t, config.Config{
		Repos: map[string]config.Repo{
			"repo1": {Path: "/tmp/repo1", Groups: []string{"work", "oss"}},
			"repo2": {Path: "/tmp/repo2", Groups: []string{"work"}},
		},
	})

	err := runHRD(t, cfgPath, []string{"group", "rm", "work", "repo1"})
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"oss"}, cfg.Repos["repo1"].Groups)
	assert.NotContains(t, cfg.Groups["work"].Repos, "repo1")

	err = runHRD(t, cfgPath, []string{"group", "rm", "nonexistent", "repo1"})
	require.NoError(t, err)
}

// TestGroupRmMultipleRepos verifies the group-first argument order allows
// removing several repos from one group in a single invocation.
func TestGroupRmMultipleRepos(t *testing.T) {
	cfgPath := setupTestConfig(t, config.Config{
		Repos: map[string]config.Repo{
			"repo1": {Path: "/tmp/repo1", Groups: []string{"work"}},
			"repo2": {Path: "/tmp/repo2", Groups: []string{"work"}},
		},
	})

	err := runHRD(t, cfgPath, []string{"group", "rm", "work", "repo1", "repo2"})
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Empty(t, cfg.Repos["repo1"].Groups)
	assert.Empty(t, cfg.Repos["repo2"].Groups)
}

func TestGroupRmUnknownRepo(t *testing.T) {
	root := t.TempDir()
	discovertest.FakeGitDirAt(t, filepath.Join(root, "beta"))
	cfgPath := setupTestConfig(t, config.Config{
		Roots: map[string]config.Root{"owner": {Path: root, Groups: []string{"work"}}},
	})

	for _, name := range []string{"beta", "nonexistent"} {
		err := runHRD(t, cfgPath, []string{"group", "rm", "work", name})
		require.ErrorIs(t, err, errUnknownRepo)
	}
}

// TestGroupRmValidatesGroupName is a symmetry regression test: "group rm"
// now validates the group name the same way "group add" does (the old
// "repo ungroup" skipped this).
func TestGroupRmValidatesGroupName(t *testing.T) {
	cfgPath := setupTestConfig(t, config.Config{
		Repos: map[string]config.Repo{"repo1": {Path: "/tmp/repo1"}},
	})

	err := runHRD(t, cfgPath, []string{"group", "rm", "@@none", "repo1"})
	require.Error(t, err)
}
