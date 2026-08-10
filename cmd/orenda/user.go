// Package main — `orenda user` subcommands (Phase 1 bootstrap: create).
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
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

	repo := sqlite.NewUserRepository(db)
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

	users, err := sqlite.NewUserRepository(db).List(cmd.Context())
	if err != nil {
		return fmt.Errorf("user list: %w", err)
	}
	if len(users) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no users configured")
		return nil
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tEMAIL\tROLE\tDISPLAY_NAME\tCREATED")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
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

	repo := sqlite.NewUserRepository(db)

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

	fmt.Fprintf(cmd.OutOrStdout(),
		"password reset for %s (id=%s)\n", target.Email, target.ID,
	)
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
	if !term.IsTerminal(int(syscall.Stdin)) {
		return "", errors.New("stdin is not a TTY; use --password-stdin to read from a pipe")
	}
	fmt.Fprint(os.Stderr, "Password: ")
	pw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(pw), nil
}
