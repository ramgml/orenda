// Package main — `orenda user` subcommands (Phase 1 bootstrap: create).
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

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

	cmd.AddCommand(newUserCreateCmd())
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
