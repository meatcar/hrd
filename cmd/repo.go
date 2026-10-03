package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hugoh/hrd/internal/backend"
	"github.com/hugoh/hrd/internal/config"
	"github.com/hugoh/hrd/internal/runner"
	"github.com/hugoh/hrd/internal/ui"
	"github.com/spf13/cobra"
)

var (
	errAtLeastOnePath  = errors.New("at least one path required")
	errNameSingleRepo  = errors.New("--name can only be used when adding a single repo")
	errAtLeastOneName  = errors.New("at least one repo name required")
	errRepoRenameUsage = errors.New("usage: repo rename <old> <new>")
	errUnknownRepo     = errors.New("unknown repo")
	errRepoExists      = errors.New("repo already exists")
	errRepoNoVCS       = errors.New("no VCS detected")
	errUnknownGroup    = errors.New("unknown group")
	errGroupAddUsage   = errors.New("usage: group add <group> <repo> [<repo>...]")
	errGroupRmUsage    = errors.New("usage: group rm <group> <repo> [<repo>...]")
)

// repoCommands returns the `repo` subcommand with its children.
func repoCommands(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdNameRepo,
		Short: "manage tracked repositories",
	}
	cmd.AddCommand(
		repoAddCmd(cfgPath),
		repoScanCmd(cfgPath),
		repoRootCmd(cfgPath),
		repoRemoveCmd(cfgPath),
		repoListCmd(cfgPath),
		repoRenameCmd(cfgPath),
	)

	return cmd
}

func repoAddCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:               cmdNameAdd + " <path>...",
		Short:             "add one or more repositories",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: dirsOnlyCompleter,
		RunE:              repoAddAction(cfgPath),
	}
	cmd.Flags().StringP("name", "n", "", "explicit name (only valid when adding a single repo)")
	cmd.Flags().StringSliceP(cmdNameGroup, "g", nil, "add the repo(s) to these groups (repeatable)")

	return cmd
}

// addFlags holds the --name and --group flags shared by "repo add" and
// "repo root add".
type addFlags struct {
	name   string
	groups []string
}

// prepareAdd resolves the --name/--group flags and loads config for an
// "add" command, in the order needed to report the most relevant error
// first: name/count mismatch, then config load, then group validity.
func prepareAdd(
	cfgPath *string, cmd *cobra.Command, args []string, label string,
) (addFlags, config.Config, error) {
	name, err := singleNameFlag(cmd, args)
	if err != nil {
		return addFlags{}, config.Config{}, err
	}

	cfg, err := loadConfig(cfgPath, label)
	if err != nil {
		return addFlags{}, config.Config{}, err
	}

	groups, err := validGroupFlags(cmd)
	if err != nil {
		return addFlags{}, config.Config{}, err
	}

	return addFlags{name: name, groups: groups}, cfg, nil
}

func repoAddAction(cfgPath *string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return errAtLeastOnePath
		}

		flags, cfg, err := prepareAdd(cfgPath, cmd, args, "repo add")
		if err != nil {
			return err
		}

		for _, arg := range args {
			if err := addRepo(&cfg, arg, flags.name, flags.groups); err != nil {
				return err
			}
		}

		return config.Save(*cfgPath, cfg)
	}
}

// singleNameFlag returns the --name flag's value, rejecting it when more
// than one arg is given since an explicit name can't apply to several.
func singleNameFlag(cmd *cobra.Command, args []string) (string, error) {
	name := flagString(cmd, "name")
	if name != "" && len(args) > 1 {
		return "", errNameSingleRepo
	}

	return name, nil
}

// validGroupFlags returns the --group flag's values, each stripped of its
// '@' prefix and validated as a group name.
func validGroupFlags(cmd *cobra.Command) ([]string, error) {
	raw, _ := cmd.Flags().GetStringSlice(cmdNameGroup)

	groups := make([]string, 0, len(raw))
	for _, g := range raw {
		group := stripGroupPrefix(g)
		if err := config.ValidGroupName(group); err != nil {
			return nil, err //nolint:wrapcheck // config error already has context
		}

		groups = append(groups, group)
	}

	return groups, nil
}

// addToGroups adds name to each group and logs the addition.
func addToGroups(cfg *config.Config, path, name string, groups []string) {
	for _, group := range groups {
		cfg.AddRepoToGroup(name, group)
	}

	if len(groups) > 0 {
		ui.Infof("added %s as %q in group %s", path, name, strings.Join(groups, ", "))
	} else {
		ui.Infof("added %s as %q", path, name)
	}
}

// addRepo validates and registers a single repo path in cfg. An empty
// explicitName derives the name from the directory base name.
func addRepo(cfg *config.Config, path, explicitName string, groups []string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving %q: %w", path, err)
	}

	if _, err := backend.Detect(abs); err != nil {
		return fmt.Errorf("%s: %w", abs, errRepoNoVCS)
	}

	name := explicitName
	if name == "" {
		name = filepath.Base(abs)
	}

	if _, exists := cfg.Repos[name]; exists {
		return fmt.Errorf(
			"%w %q (path: %s). use --name/-n to specify a unique name",
			errRepoExists,
			name,
			cfg.Repos[name].Path,
		)
	}

	cfg.AddRepo(name, config.Repo{Path: abs})
	addToGroups(cfg, abs, name, groups)

	return nil
}

func repoRemoveCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:               "rm <name>...",
		Short:             "remove one or more repositories",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: reposOnlyCompleter(cfgPath),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errAtLeastOneName
			}

			cfg, err := loadConfig(cfgPath, "repo rm")
			if err != nil {
				return err
			}

			for _, name := range args {
				if _, ok := cfg.Repos[name]; !ok {
					return fmt.Errorf("%w %q", errUnknownRepo, name)
				}

				cfg.RemoveRepo(name)
				ui.Infof("removed %q", name)
			}

			return config.Save(*cfgPath, cfg)
		},
	}
}

func repoListCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:               "ls [group]",
		Short:             "list tracked repositories, optionally filtered to one group",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: groupsOnlyCompleter(cfgPath),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := loadResolvedConfig(cfgPath, "repo ls")
			if err != nil {
				return err
			}

			names := make([]string, 0, len(cfg.Repos))

			if raw := firstArg(args); raw != "" {
				grp, ok := cfg.GroupRepos(groupQuery(raw))
				if !ok {
					return fmt.Errorf("%w %q", errUnknownGroup, raw)
				}

				names = grp
			} else {
				for name := range cfg.Repos {
					names = append(names, name)
				}

				slices.Sort(names)
			}

			const nameWidth = 15

			const vcsWidth = 3

			const gap = 2

			pathWidth := ui.GetTermWidth() - nameWidth - vcsWidth - gap

			rows := make([][]string, 0, len(names))
			for _, name := range names {
				repo := cfg.Repos[name]

				vcsLabel := repo.ActiveBackend()

				rows = append(rows, []string{name, vcsLabel, repo.Path})
			}

			widths := []int{nameWidth, vcsWidth, pathWidth}
			header := []string{NameLabel, VCSLabel, PathLabel}

			ui.Print(ui.RenderTable(
				header, rows, ui.EffectiveWidths(header, rows, widths),
			))

			return nil
		},
	}
}

const (
	cmdNameRepo   = "repo"
	cmdNameAdd    = "add"
	cmdNameRename = "rename"
	cmdNameGroup  = "group"
)

// completeFirstArgWithRepos completes the first (and only the first)
// positional arg with repo names.
func completeFirstArgWithRepos(cfgPath *string) cobraCompleter {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return reposOnlyCompleter(cfgPath)(cmd, args, toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeFirstArgWithGroups completes the first positional arg with group
// names; every subsequent arg completes with repo names or directories (see
// "hrd group add/rm <group> <repo>...").
func completeFirstArgWithGroups(cfgPath *string) cobraCompleter {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return groupsOnlyCompleter(cfgPath)(cmd, args, toComplete)
		}

		return reposOrDirsCompleter(cfgPath)(cmd, args, toComplete)
	}
}

func repoRenameCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:               cmdNameRename + " <old-name> <new-name>",
		Short:             "rename a repository",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeFirstArgWithRepos(cfgPath),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 { //nolint:mnd // expects old and new name
				return errRepoRenameUsage
			}

			oldName, newName := args[0], args[1]

			cfg, err := loadConfig(cfgPath, "repo rename")
			if err != nil {
				return err
			}

			repo, ok := cfg.Repos[oldName]
			if !ok {
				return fmt.Errorf("%w %q", errUnknownRepo, oldName)
			}

			if _, exists := cfg.Repos[newName]; exists {
				return fmt.Errorf("%w %q", errRepoExists, newName)
			}

			cfg.RemoveRepo(oldName)
			cfg.AddRepo(newName, repo)

			ui.Infof("renamed %q → %q", oldName, newName)

			return config.Save(*cfgPath, cfg)
		},
	}
}

// stripGroupPrefix removes a leading '@' from a group name if present.
// This lets users type @work or work interchangeably on the CLI.
func stripGroupPrefix(name string) string {
	return strings.TrimPrefix(name, "@")
}

// groupQuery normalizes a raw group-lookup argument for cfg.GroupRepos: a
// reserved "@@"-prefixed token (e.g. "@@none") is passed through unchanged,
// since stripping a single '@' would mangle it; anything else gets the
// normal single-'@' strip.
func groupQuery(raw string) string {
	if config.IsReservedGroupName(raw) {
		return raw
	}

	return stripGroupPrefix(raw)
}

// displayGroup adds a '@' prefix for display purposes so group names
// are visually distinguishable from repo names in output.
func displayGroup(name string) string {
	if !strings.HasPrefix(name, "@") {
		return "@" + name
	}

	return name
}

// resolveRepoArg maps a "group add/rm" repo argument to a configured repo
// name. It accepts either the repo name directly or a filesystem path
// (e.g. ".") pointing at a configured repo's root.
func resolveRepoArg(cfg *config.Config, arg string) (string, error) {
	if _, ok := cfg.Repos[arg]; ok {
		return arg, nil
	}

	if name, ok := cfg.RepoNameForPath(arg); ok {
		return name, nil
	}

	return "", fmt.Errorf("%w %q", errUnknownRepo, arg)
}

// groupMemberAction returns a RunE for "hrd group add/rm <group> <repo>...":
// validates the group name, loads config, applies act to each repo in turn
// (failing fast on an unknown repo), and saves once at the end.
func groupMemberAction(
	cfgPath *string,
	cmdLabel string,
	usageErr error,
	includeDiscovered bool,
	act func(*config.Config, string, string),
) func(cmd *cobra.Command, args []string) error {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < 2 { //nolint:mnd // expects a group and at least one repo
			return usageErr
		}

		group := stripGroupPrefix(args[0])
		if err := config.ValidGroupName(group); err != nil {
			return err //nolint:wrapcheck // config error already has context
		}

		cfg, err := loadConfig(cfgPath, cmdLabel)
		if err != nil {
			return err
		}

		resolved := cfg
		if includeDiscovered {
			resolved.Repos = maps.Clone(cfg.Repos)
			for _, warning := range resolved.ResolveRoots() {
				ui.Warnf("%v", warning)
			}
		}

		for _, repoArg := range args[1:] {
			repoName, err := resolveRepoArg(&resolved, repoArg)
			if err != nil {
				return err
			}

			if _, explicit := cfg.Repos[repoName]; !explicit {
				cfg.Repos[repoName] = config.Repo{Path: resolved.Repos[repoName].Path}
			}

			act(&cfg, repoName, group)
		}

		return config.Save(*cfgPath, cfg)
	}
}

func groupAddCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:               cmdNameAdd + " <group> <repo>...",
		Short:             "add one or more repos to a group",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeFirstArgWithGroups(cfgPath),
		RunE: groupMemberAction(cfgPath, "group add", errGroupAddUsage, true,
			func(cfg *config.Config, name, group string) {
				cfg.AddRepoToGroup(name, group)
				ui.Infof("added %q to group %q", name, group)
			}),
	}
}

func groupRmCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:               "rm <group> <repo>...",
		Short:             "remove one or more repos from a group",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeFirstArgWithGroups(cfgPath),
		RunE: groupMemberAction(cfgPath, "group rm", errGroupRmUsage, false,
			func(cfg *config.Config, name, group string) {
				cfg.RemoveRepoFromGroup(name, group)
				ui.Infof("removed %q from group %q", name, group)
			}),
	}
}

// groupCommands returns the `group` subcommand (read-only). It also accepts
// its own --list-reserved flag (printing what the "@@" reserved pseudo-
// groups mean) since that's a fast, static lookup unrelated to any specific
// group listing.
func groupCommands(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdNameGroup,
		Short: "manage repo groups",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flagBool(cmd, "list-reserved") {
				return renderReservedGroupMeanings()
			}

			return cmd.Help()
		},
	}
	cmd.Flags().Bool("list-reserved", false, `list reserved "@@" pseudo-groups and what they mean`)
	cmd.AddCommand(
		groupListCmd(cfgPath),
		groupAddCmd(cfgPath),
		groupRmCmd(cfgPath),
	)

	return cmd
}

func groupListCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls [name]",
		Short: "list groups",
		Args:  cobra.ArbitraryArgs,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return groupsOnlyCompleter(cfgPath)(cmd, args, toComplete)
			}

			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: listGroupsAction(cfgPath),
	}
	cmd.Flags().Bool(
		"live", false,
		`also compute repo membership for reserved groups that require live git/jj status (e.g. @@attention)`,
	)

	return cmd
}

func listGroupsAction(cfgPath *string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := loadResolvedConfig(cfgPath, "group ls")
		if err != nil {
			return err
		}

		if raw := firstArg(args); raw != "" {
			name := groupQuery(raw)

			repos, ok := cfg.GroupRepos(name)
			if !ok {
				return fmt.Errorf("%w %q", errUnknownGroup, displayGroup(name))
			}

			if name == config.ReservedAttention {
				repos = attentionRepos(cmd.Context(), cfg, repos)
			}

			for _, repo := range repos {
				ui.Out(repo)
			}

			return nil
		}

		if len(cfg.Groups) == 0 {
			ui.Out("no groups defined")
		} else if err := renderGroupTable(cfg); err != nil {
			return err
		}

		return renderReservedGroupMemberships(cmd.Context(), cfg, flagBool(cmd, "live"))
	}
}

func renderGroupTable(cfg config.Config) error {
	for name, group := range cfg.Groups {
		ui.Out(displayGroup(name))
		ui.Out("  " + strings.Join(group.Repos, ", "))
	}

	return nil
}

// renderReservedGroupMemberships prints each reserved "@@" pseudo-group
// alongside the repos it currently contains, in the same visual format as
// renderGroupTable, so they read as "more groups in the list" rather than a
// separate concept. Live groups (e.g. @@attention) only have their
// membership computed when live is true; otherwise a hint is printed
// instead, keeping the default "hrd group ls" a fast, config-only read.
func renderReservedGroupMemberships(ctx context.Context, cfg config.Config, live bool) error {
	for _, rg := range config.ReservedGroups {
		if rg.Live && !live {
			ui.Out(rg.Name + "  (pass --live to compute)")

			continue
		}

		repos, ok := cfg.GroupRepos(rg.Name)
		if !ok {
			continue
		}

		if rg.Name == config.ReservedAttention {
			repos = attentionRepos(ctx, cfg, repos)
		}

		ui.Out(rg.Name)

		if len(repos) == 0 {
			ui.Out("  (none)")
		} else {
			ui.Out("  " + strings.Join(repos, ", "))
		}
	}

	return nil
}

// attentionRepos narrows candidates down to repos currently needing
// attention (dirty, or ahead/behind/diverged/gone vs. their remote),
// gathering live status in parallel. Repos whose status can't be read are
// excluded with a warning, matching applyStatusFilter's behavior.
func attentionRepos(ctx context.Context, cfg config.Config, candidates []string) []string {
	statuses := make(map[string]runner.StatusResult, len(candidates))

	ch := runner.GatherStatus(ctx, cfg.Repos, candidates, cfg.Settings.Concurrency)
	for res := range ch {
		statuses[res.RepoName] = res
	}

	var matched []string

	for _, name := range candidates {
		res := statuses[name]
		if res.Err != nil {
			ui.Warnf("%s: %v", name, res.Err)

			continue
		}

		if res.Status.NeedsAttention() {
			matched = append(matched, name)
		}
	}

	return matched
}

// renderReservedGroupMeanings prints the "@@" pseudo-groups and what they
// mean, for "hrd group --list-reserved".
func renderReservedGroupMeanings() error {
	rows := make([][]string, 0, len(config.ReservedGroups))
	for _, rg := range config.ReservedGroups {
		rows = append(rows, []string{rg.Name, rg.Desc})
	}

	const nameWidth = len("@@attention") + 2

	header := []string{"GROUP", "MEANING"}
	widths := ui.EffectiveWidths(header, rows, []int{nameWidth, ui.GetTermWidth()})

	ui.Print(ui.RenderTable(header, rows, widths))

	return nil
}
