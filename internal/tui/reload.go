package tui

import (
	"fmt"
	"maps"

	tea "charm.land/bubbletea/v2"
	"github.com/hugoh/hrd/internal/config"
)

// The config file is read and written from Cmd goroutines, never from
// Update, so a slow disk can't freeze the UI.

// configLoadedMsg delivers a config re-read from disk.
type configLoadedMsg struct {
	cfg config.Config
	err error
}

// groupSavedMsg reports a group save. cfg includes discovered repos,
// even when they have no entry in the saved config.
type groupSavedMsg struct {
	cfg config.Config
	err error
}

func loadConfigCmd(path string) tea.Cmd {
	return func() tea.Msg {
		fresh, _, err := config.LoadResolved(path)
		if err != nil {
			return configLoadedMsg{err: fmt.Errorf("reloading config: %w", err)}
		}

		return configLoadedMsg{cfg: fresh}
	}
}

func (m *model) handleConfigLoaded(msg configLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.modal = modalAlert
		m.alertMsg = "reload config failed: " + msg.err.Error()
	} else {
		m.applyConfig(msg.cfg)
	}

	m.loading = true

	cmd := loadStatusesCmd(m)
	m.updateTableRows()

	return m, cmd
}

// applyConfig swaps in a freshly loaded config. New repos are auto-selected
// only if the whole prior selection was "all" — treating that as a standing
// mode rather than a frozen snapshot — otherwise a refresh could silently
// expand a curated selection.
func (m *model) applyConfig(fresh config.Config) {
	newSelected := m.rebuildSelection(fresh)

	newGroupFilter := m.groupFilter
	if newGroupFilter != "" {
		if _, ok := fresh.Groups[newGroupFilter]; !ok {
			newGroupFilter = ""
		}
	}

	cur := m.tableRepos()

	cursorName := ""
	if m.cursor >= 0 && m.cursor < len(cur) {
		cursorName = cur[m.cursor]
	}

	m.cfg = fresh
	m.repoOrder = sortedRepoKeys(fresh.Repos)
	m.selected = newSelected
	m.groupFilter = newGroupFilter
	m.updateTableRows()

	m.restoreCursorByName(cursorName)
}

func (m *model) rebuildSelection(fresh config.Config) map[string]bool {
	wasAllSelected := true

	for _, name := range m.repoOrder {
		if !m.selected[name] {
			wasAllSelected = false

			break
		}
	}

	newSelected := make(map[string]bool, len(m.selected))

	for name, sel := range m.selected {
		if _, ok := fresh.Repos[name]; ok {
			newSelected[name] = sel
		}
	}

	if wasAllSelected {
		for name := range fresh.Repos {
			if _, ok := newSelected[name]; !ok {
				newSelected[name] = true
			}
		}
	}

	return newSelected
}

func (m *model) restoreCursorByName(name string) {
	if name == "" {
		return
	}

	for i, n := range m.tableRepos() {
		if n == name {
			m.cursor = i
			m.repoTable.SetCursor(i)

			return
		}
	}
}

// mutateConfigFile reloads the config to preserve edits made since the TUI
// loaded it. mutate must check that its target repos still exist.
// Discovery runs after saving to avoid writing every discovered repo to disk.
func mutateConfigFile(path string, mutate func(cfg *config.Config)) (config.Config, error) {
	fresh, err := config.Load(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("reloading config: %w", err)
	}

	mutate(&fresh)

	if err := config.Save(path, fresh); err != nil {
		return config.Config{}, fmt.Errorf("saving config: %w", err)
	}

	fresh.ResolveRoots()

	return fresh, nil
}

// addToGroup adds memberships for repos still tracked by the config or its
// directory roots. New entries contain only the requested group.
func addToGroup(names []string, group string) func(cfg *config.Config) {
	return func(cfg *config.Config) {
		resolved := *cfg
		resolved.Repos = maps.Clone(cfg.Repos)
		resolved.ResolveRoots()

		for _, repoName := range names {
			repo, ok := resolved.Repos[repoName]
			if !ok {
				continue
			}

			if _, explicit := cfg.Repos[repoName]; !explicit {
				cfg.Repos[repoName] = config.Repo{Path: repo.Path}
			}

			cfg.AddRepoToGroup(repoName, group)
		}
	}
}

// saveGroupCmd adds the currently selected repos to group in the config file.
func (m *model) saveGroupCmd(group string) tea.Cmd {
	path, names := m.opts.ConfigPath, m.selectedNames()

	return func() tea.Msg {
		fresh, err := mutateConfigFile(path, addToGroup(names, group))

		return groupSavedMsg{cfg: fresh, err: err}
	}
}

func (m *model) handleGroupSaved(msg groupSavedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.modal = modalAlert
		m.alertMsg = "save failed: " + msg.err.Error()

		return m, nil
	}

	m.cfg = msg.cfg

	if m.screen == screenGroup {
		m.groupNewInput = false
		m.screen = screenMain
	}

	return m, nil
}
