package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hugoh/hrd/internal/config"
	"github.com/hugoh/hrd/internal/discover/discovertest"
	"github.com/stretchr/testify/require"
)

// feed runs cmd and delivers every message it produces back through Update,
// standing in for the Bubble Tea runtime.
func feed(m *model, cmd tea.Cmd) {
	for _, msg := range flattenMsgs(cmd) {
		if msg != nil {
			m.Update(msg)
		}
	}
}

func TestRefreshReadsConfigOffUpdate(t *testing.T) {
	m, cfgPath := newAlphaModel(t)
	writeConfigFile(t, cfgPath, `[repos.alpha]
path = "`+t.TempDir()+`"

[repos.beta]
path = "`+t.TempDir()+`"
`)

	_, cmd := m.handleKeyMsg(tea.KeyPressMsg{Code: 'r', Text: "r"})

	require.NotNil(t, cmd)
	require.True(t, m.loading)
	require.NotContains(t, m.cfg.Repos, "beta", "Update itself must not read the config file")

	loaded, ok := cmd().(configLoadedMsg)
	require.True(t, ok)
	require.NoError(t, loaded.err)

	_, next := m.Update(loaded)

	require.Contains(t, m.cfg.Repos, "beta")
	require.NotNil(t, next, "applying the config kicks off a status refresh")
}

func TestRefreshWithBrokenConfigAlertsAndKeepsState(t *testing.T) {
	m, cfgPath := newAlphaModel(t)
	writeConfigFile(t, cfgPath, `not valid toml [[[`)

	_, cmd := m.handleKeyMsg(tea.KeyPressMsg{Code: 'r', Text: "r"})
	loaded, ok := cmd().(configLoadedMsg)
	require.True(t, ok)
	require.Error(t, loaded.err)

	_, next := m.Update(loaded)

	require.Equal(t, modalAlert, m.modal)
	require.Contains(t, m.alertMsg, "reload config failed")
	require.Equal(t, []string{"alpha"}, m.repoOrder)
	require.NotNil(t, next, "statuses still refresh from the old config")
}

func TestGroupAddSavesOffUpdate(t *testing.T) {
	m, cfgPath := newAlphaModel(t)
	m.selected["alpha"] = true
	m.screen = screenGroup

	_, cmd := m.handleGroupAddSelect("work")

	require.NotNil(t, cmd)
	require.NotContains(t, m.cfg.Groups, "work", "Update must not write the config file")

	onDiskBefore, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.NotContains(t, onDiskBefore.Groups, "work")

	saved, ok := cmd().(groupSavedMsg)
	require.True(t, ok)
	require.NoError(t, saved.err)

	m.Update(saved)

	require.Contains(t, m.cfg.Groups, "work")
	require.Equal(t, screenMain, m.screen)
}

func TestGroupAddRootDiscoveredRepo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		discovertest.FakeGitDirAt(t, filepath.Join(root, name))
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, config.Save(cfgPath, config.Config{
		Roots: map[string]config.Root{"owner": {Path: root, Groups: []string{"owner"}}},
	}))

	m, err := newTestModel(t.Context(), t, Options{ConfigPath: cfgPath, Group: "owner"})
	require.NoError(t, err)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	for _, key := range []tea.KeyPressMsg{
		{Code: 'x'},
		{Code: tea.KeySpace},
		{Code: tea.KeyEnter},
		{Code: 'g'},
		{Code: tea.KeyDown},
		{Code: tea.KeyEnter},
		{Code: 'f', Text: "focus"},
		{Code: tea.KeyEnter},
	} {
		_, cmd := m.Update(key)
		feed(m, cmd)
	}

	require.Equal(t, []string{"beta"}, m.cfg.Groups["focus"].Repos)
	require.Equal(t, []string{"alpha", "beta"}, m.cfg.Groups["owner"].Repos)

	onDisk, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Equal(t, map[string]config.Repo{
		"beta": {Path: filepath.Join(root, "beta"), Groups: []string{"focus"}},
	}, onDisk.Repos, "only the selected repo and its explicit group should be persisted")

	m.Update(tea.KeyPressMsg{Code: '@'})
	require.Len(t, m.groupList.Items(), 4)
	require.Equal(t, "focus", m.groupList.Items()[2].FilterValue())
	require.Equal(t, "owner", m.groupList.Items()[3].FilterValue())
	m.groupList.Select(2)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, []string{"beta"}, m.tableRepos())

	restarted, err := newTestModel(t.Context(), t, Options{ConfigPath: cfgPath, Group: "focus"})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, restarted.repoOrder)
	require.Equal(t, []string{"beta"}, restarted.tableRepos())
	require.Equal(t, []string{"alpha", "beta"}, restarted.cfg.Groups["owner"].Repos)
}

func TestGroupAddFailureAlertsAndStaysOnGroupScreen(t *testing.T) {
	m, cfgPath := newAlphaModel(t)
	m.selected["alpha"] = true
	m.screen = screenGroup

	makeDirReadOnly(t, filepath.Dir(cfgPath))

	_, cmd := m.handleGroupAddSelect("work")
	feed(m, cmd)

	require.Equal(t, modalAlert, m.modal)
	require.Contains(t, m.alertMsg, "save failed")
	require.Equal(t, screenGroup, m.screen)
}

func TestSelectionChangeSavesStateOffUpdate(t *testing.T) {
	m := newAlphaBetaModel(t)
	m.stateFile = filepath.Join(t.TempDir(), "state.json")
	m.mode = modeSelect
	m.selected = map[string]bool{}
	m.updateTableRows()

	_, cmd := m.handleSelectOne()

	require.NotNil(t, cmd)
	require.NoFileExists(t, m.stateFile, "Update must not write the state file")

	cmd()

	state, err := loadState(m.stateFile)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha"}, state.LastRepos)
}

func TestStateWriterDropsStaleWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	w := &stateWriter{}

	require.NoError(t, w.write(path, 2, PersistentState{LastGroup: "new"}))
	require.NoError(t, w.write(path, 1, PersistentState{LastGroup: "old"}))

	state, err := loadState(path)
	require.NoError(t, err)
	require.Equal(t, "new", state.LastGroup, "an older snapshot must not overwrite a newer one")
}

func TestQuitFlushesStateBeforeReturning(t *testing.T) {
	m := newAlphaBetaModel(t)
	m.stateFile = filepath.Join(t.TempDir(), "state.json")
	m.groupFilter = "work"

	m.quit()

	_, err := os.Stat(m.stateFile)
	require.NoError(t, err, "quit must persist before the program exits")
}
