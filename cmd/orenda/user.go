// Package main — `orenda user` subcommands (create, list, reset-password,
// delete).
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage"
)

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "User management",
	}

	cmd.AddCommand(
		newUserCreateCmd(),
		newUserListCmd(),
		newUserResetPasswordCmd(),
		newUserDeleteCmd(),
	)
	return cmd
}

func newUserCreateCmd() *cobra.Command {
	var (
		email       string
		displayName string
		role        string
		fromStdin   bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new user (Phase 1: bootstrap the single owner)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserCreate(cmd, userCreateInput{
				Email:       email,
				DisplayName: displayName,
				Role:        role,
				FromStdin:   fromStdin,
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "user email (required)")
	cmd.Flags().StringVar(&displayName, "display-name", "", "display name (required)")
	cmd.Flags().StringVar(&role, "role", "owner", "user role (Phase 1: owner only)")
	cmd.Flags().BoolVar(&fromStdin, "password-stdin", false, "read password from stdin instead of prompting")
	return cmd
}

type userCreateInput struct {
	Email       string
	DisplayName string
	Role        string
	FromStdin   bool
}

// runUserCreate is split out from the cobra RunE so tests can drive it
// without spawning a subprocess.
func runUserCreate(cmd *cobra.Command, in userCreateInput) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return err
	}

	if in.Email == "" || in.DisplayName == "" {
		return errors.New("user create: --email and --display-name are required")
	}
	plain, err := readPassword(cmd, in.FromStdin)
	if err != nil {
		return err
	}
	if len(plain) < 8 {
		return errors.New("user create: password must be at least 8 characters")
	}

	hash, err := auth.HashPassword(plain, cfg.Auth.BcryptCost)
	if err != nil {
		return fmt.Errorf("user create: hash: %w", err)
	}
	_ = plain // hint for go vet: drop reference early

	db, cleanup, err := openCLIDB(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	repo := storage.NewUserRepository(db)
	u := &user.User{
		Email:        in.Email,
		PasswordHash: hash,
		DisplayName:  in.DisplayName,
		Role:         user.Role(in.Role),
	}
	if err := repo.Create(cmd.Context(), u); err != nil {
		return fmt.Errorf("user create: %w", err)
	}

	fmt.Printf("user created: id=%s email=%s role=%s\n", u.ID, u.Email, u.Role)
	return nil
}

// newUserListCmd implements `orenda user list`, a single-owner
// diagnostic to help users verify which account is configured after a
// fresh install or a password reset.
func newUserListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured users",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserList(cmd)
		},
	}
	return cmd
}

func runUserList(cmd *cobra.Command) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return err
	}
	db, cleanup, err := openCLIDB(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	users, err := storage.NewUserRepository(db).List(cmd.Context())
	if err != nil {
		return fmt.Errorf("user list: %w", err)
	}
	if len(users) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no users configured") // stdout status line
		return nil
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tEMAIL\tROLE\tDISPLAY_NAME\tCREATED") // row errors surface via Flush below
	for _, u := range users {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			u.ID, u.Email, u.Role, u.DisplayName, u.CreatedAt.Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}

// newUserResetPasswordCmd implements `orenda user reset-password`. This
// exists because Orenda is single-owner / local-only: there is no
// email recovery flow, so the owner needs a CLI escape hatch to
// re-establish access without losing data.
func newUserResetPasswordCmd() *cobra.Command {
	var (
		email     string
		fromStdin bool
	)
	cmd := &cobra.Command{
		Use:   "reset-password",
		Short: "Reset a user's password (recovery for local-only installs)",
		Long: strings.TrimSpace(`
Reset a user's password by email. Used when the owner forgets the
password and there is no email recovery flow (Orenda is local-only).

Examples:
  orenda user reset-password --email you@example.com
  echo 'new-password' | orenda user reset-password --email you@example.com --password-stdin

If --email is omitted and exactly one user exists, that user is reset.
`),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserResetPassword(cmd, userResetPasswordInput{
				Email:     email,
				FromStdin: fromStdin,
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "user email (defaults to the only configured user)")
	cmd.Flags().BoolVar(&fromStdin, "password-stdin", false, "read new password from stdin instead of prompting")
	return cmd
}

type userResetPasswordInput struct {
	Email     string
	FromStdin bool
}

// runUserResetPassword changes a user's password hash. The CLI is the
// only sanctioned path because the API must not expose password writes
// to network callers.
func runUserResetPassword(cmd *cobra.Command, in userResetPasswordInput) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return err
	}

	newPassword, err := readPassword(cmd, in.FromStdin)
	if err != nil {
		return err
	}
	if len(newPassword) < 8 {
		return errors.New("user reset-password: password must be at least 8 characters")
	}

	db, cleanup, err := openCLIDB(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	repo := storage.NewUserRepository(db)

	var target *user.User
	if in.Email != "" {
		target, err = repo.GetByEmail(cmd.Context(), strings.ToLower(in.Email))
	} else {
		// Single-owner convenience: when --email is omitted and exactly
		// one *real* (non-system, role != "system") user exists, reset
		// that one. The migrations seed a `system` placeholder user
		// (role='system') which we filter out — it's never used for
		// login. Refuse to guess otherwise.
		users, lerr := repo.List(cmd.Context())
		if lerr != nil {
			return fmt.Errorf("user reset-password: list: %w", lerr)
		}
		var owners []*user.User
		for _, u := range users {
			if u.Role != user.RoleSystem {
				owners = append(owners, u)
			}
		}
		switch len(owners) {
		case 0:
			return errors.New("user reset-password: no users configured; create one with `orenda user create`")
		case 1:
			target = owners[0]
		default:
			return fmt.Errorf("user reset-password: multiple users configured; pass --email to pick one")
		}
	}
	if err != nil {
		return fmt.Errorf("user reset-password: lookup: %w", err)
	}

	hash, err := auth.HashPassword(newPassword, cfg.Auth.BcryptCost)
	if err != nil {
		return fmt.Errorf("user reset-password: hash: %w", err)
	}
	target.PasswordHash = hash

	if err := repo.Update(cmd.Context(), target); err != nil {
		return fmt.Errorf("user reset-password: update: %w", err)
	}
	_ = newPassword // hint for go vet: drop reference early

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"password reset for %s (id=%s)\n", target.Email, target.ID,
	) // stdout status line
	return nil
}

// readPassword reads the password from the terminal or stdin.
//
// With --password-stdin the input is read once and trimmed (suitable for
// piped input from `echo` or a secrets manager). Without the flag the
// terminal is put into raw mode so the password is not echoed.
func readPassword(cmd *cobra.Command, fromStdin bool) (string, error) {
	if fromStdin {
		scanner := bufio.NewScanner(cmd.InOrStdin())
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			return "", errors.New("read stdin: empty input")
		}
		return strings.TrimRight(scanner.Text(), "\r\n"), nil
	}
	stdinFd := stdinFD()
	if !term.IsTerminal(stdinFd) {
		return "", errors.New("stdin is not a TTY; use --password-stdin to read from a pipe")
	}
	fmt.Fprint(os.Stderr, "Password: ")
	pw, err := term.ReadPassword(stdinFd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(pw), nil
}

// newUserDeleteCmd implements `orenda user delete`, the test-user cleanup
// escape hatch. Deleting a user is destructive and mostly irreversible
// (owned courses cascade with the row), so the command is guarded: it
// previews without --yes, refuses to touch the synthetic agent-owner,
// refuses to strand the install without a real owner, and refuses to
// silently drop active courses without --force.
func newUserDeleteCmd() *cobra.Command {
	var (
		email     string
		id        string
		assumeYes bool
		force     bool
	)

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a user (test-user cleanup)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserDelete(cmd, userDeleteInput{
				Email:     email,
				ID:        id,
				AssumeYes: assumeYes,
				Force:     force,
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email of the user to delete")
	cmd.Flags().StringVar(&id, "id", "", "id of the user to delete")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "actually delete (omit to preview)")
	cmd.Flags().BoolVar(&force, "force", false, "also delete the user's courses (required while any course is active)")
	return cmd
}

type userDeleteInput struct {
	Email     string
	ID        string
	AssumeYes bool
	Force     bool
}

// runUserDelete is split out from the cobra RunE so tests can drive it
// without spawning a subprocess.
func runUserDelete(cmd *cobra.Command, in userDeleteInput) error {
	// Exactly one of --email | --id, like rm needs exactly one operand.
	if (in.Email == "") == (in.ID == "") {
		return errors.New("user delete: pass exactly one of --email or --id")
	}

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return err
	}

	db, cleanup, err := openCLIDB(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	users := storage.NewUserRepository(db)
	coursesRepo := storage.NewCourseRepository(db)

	target, err := resolveUserForDelete(cmd.Context(), users, in.Email, in.ID)
	if err != nil {
		return err
	}

	// Hard guard 1: the synthetic agent-owner (role=system, T171) backs
	// the /agent/* token auth. Deleting it would break every agent
	// session; there is no legitimate CLI reason to remove it.
	if target.Role == user.RoleSystem {
		return fmt.Errorf("user delete: %s is the synthetic agent-owner (role=system); refusing", target.Email)
	}

	// Hard guard 2: keep at least one real (non-system) owner. Single-owner
	// installs break otherwise (login, FirstNonSystem notify, migrations
	// that seed data under the first non-system user).
	all, err := users.List(cmd.Context())
	if err != nil {
		return fmt.Errorf("user delete: list: %w", err)
	}
	remaining := 0
	for _, u := range all {
		if u.ID != target.ID && u.Role != user.RoleSystem {
			remaining++
		}
	}
	if remaining == 0 {
		return errors.New("user delete: refusing to delete the last non-system owner")
	}

	owned, err := coursesRepo.ListCourses(cmd.Context(), target.ID)
	if err != nil {
		return fmt.Errorf("user delete: list courses: %w", err)
	}
	active := countActiveCourses(owned)
	blockers, err := countProjectBlockers(cmd.Context(), db, target.ID)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if !in.AssumeYes {
		// Preview guard, like rm without -f: show what WOULD happen,
		// change nothing, exit non-zero. No interactive prompts.
		// Print errors are deliberately ignored: stdout may be a closed
		// pipe, and the command's outcome is carried by the error below.
		fmt.Fprintf(out, "would delete user: id=%s email=%s role=%s courses=%d", //nolint:errcheck // best-effort preview output
			target.ID, target.Email, target.Role, len(owned))
		// Spell out the fate of non-active courses too: they are NOT
		// kept — with --yes they go with the user via the
		// courses.owner_id ON DELETE CASCADE (019), same as the active
		// ones once --force is passed. Silence here would hide that.
		if len(owned) > 0 {
			fmt.Fprintf(out, " (all %d will be deleted with the user)", len(owned)) //nolint:errcheck // best-effort preview output
		}
		if active > 0 {
			fmt.Fprintf(out, " (active=%d; re-run with --force)", active) //nolint:errcheck // best-effort preview output
		}
		fmt.Fprintln(out) //nolint:errcheck // best-effort preview output
		return errors.New("user delete: aborted (pass --yes to delete)")
	}

	// Hard guard 3 (preflight, before ANY destruction): projects.owner_id
	// (001) and project_agents.added_by (043) reference users(id) with NO
	// ON DELETE action, so a target with projects would make users.Delete
	// fail with an FK error — and under --force the courses would already
	// be gone by then. Refuse up front, leave everything untouched.
	if blockers > 0 {
		return fmt.Errorf("user delete: user owns %d project(s) or is the adder on project-agent grants; delete or reassign the projects first", blockers)
	}
	if active > 0 && !in.Force {
		return fmt.Errorf("user delete: user owns %d active course(s); pass --force to delete them along with the user", active)
	}
	if err := deleteOwnedCoursesAndUser(cmd.Context(), db, owned, target.ID, in.Force); err != nil {
		return err
	}

	fmt.Fprintf(out, "user deleted: id=%s email=%s\n", target.ID, target.Email) //nolint:errcheck // best-effort confirmation output
	return nil
}

// countActiveCourses counts courses in the active lifecycle state.
func countActiveCourses(cs []*course.Course) int {
	n := 0
	for _, c := range cs {
		if c.Status == course.StatusActive {
			n++
		}
	}
	return n
}

// countProjectBlockers counts rows that would break users.Delete with a
// raw FK error: projects owned by the target (owner_id has NO ON DELETE
// action, migration 001) and project_agents grants the target created
// (added_by, migration 043). Counted before any destructive step so a
// refusal never leaves partial damage.
func countProjectBlockers(ctx context.Context, db *sql.DB, userID string) (int, error) {
	var projects, grants int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE owner_id = ?`, userID).Scan(&projects); err != nil {
		return 0, fmt.Errorf("user delete: preflight projects: %w", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM project_agents WHERE added_by = ?`, userID).Scan(&grants); err != nil {
		return 0, fmt.Errorf("user delete: preflight project grants: %w", err)
	}
	return projects + grants, nil
}

// deleteOwnedCoursesAndUser performs the destructive phase atomically:
// per-course deletion (the DELETE /api/v1/courses/{id} handler uses
// courseRepo.DeleteCourse — a plain `DELETE FROM courses WHERE id = ?`;
// reproduced against the tx so the whole phase shares one commit — the
// cascade machinery is identical, see migrations 019/022/023) plus the
// user deletion run in ONE transaction. If any step fails (e.g. an
// unforeseen FK), the rollback restores the courses — the "courses
// deleted but user alive" window cannot happen.
func deleteOwnedCoursesAndUser(ctx context.Context, db *sql.DB, owned []*course.Course, userID string, force bool) error {
	active := countActiveCourses(owned)
	if active > 0 && !force {
		return fmt.Errorf("user delete: user owns %d active course(s); pass --force to delete them along with the user", active)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("user delete: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if force {
		for _, c := range owned {
			if _, err := tx.ExecContext(ctx, `DELETE FROM courses WHERE id = ?`, c.ID); err != nil {
				return fmt.Errorf("user delete: course %s: %w", c.ID, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("user delete: %w", err)
	}
	return tx.Commit()
}

// resolveUserForDelete finds the target by email or id and maps
// ErrNotFound to a human-readable message.
func resolveUserForDelete(ctx context.Context, users user.Repository, email, id string) (*user.User, error) {
	var (
		u   *user.User
		err error
	)
	if email != "" {
		u, err = users.GetByEmail(ctx, email)
	} else {
		u, err = users.GetByID(ctx, id)
	}
	if errors.Is(err, user.ErrNotFound) {
		return nil, fmt.Errorf("user delete: no such user (%s)", user.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("user delete: lookup: %w", err)
	}
	return u, nil
}
