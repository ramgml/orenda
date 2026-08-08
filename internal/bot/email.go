// Package bot — email transport (Phase 10.6).
package bot

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Email is a Bot that sends messages via SMTP.
type Email struct {
	Host     string // e.g. "smtp.example.com:587"
	Username string
	Password string
	From     string
	// UseTLS=true means implicit TLS (port 465); false = STARTTLS.
	UseTLS bool

	// dialer is injectable for tests.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewEmail returns an Email bot.
func NewEmail(host, username, password, from string, useTLS bool) *Email {
	return &Email{
		Host:     host,
		Username: username,
		Password: password,
		From:     from,
		UseTLS:   useTLS,
	}
}

// Name implements Bot.
func (Email) Name() string { return "email" }

// Start implements Bot.
func (Email) Start(context.Context) error { return nil }

// Stop implements Bot.
func (Email) Stop(context.Context) error { return nil }

// Send delivers the message via SMTP. target is the recipient address.
func (e *Email) Send(ctx context.Context, target string, msg Message) error {
	if e.Host == "" || e.From == "" {
		return ErrBotUnavailable
	}
	if target == "" {
		return ErrTargetMissing
	}

	body := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		e.From, target, msg.Title, msg.Body,
	)
	if msg.Link != "" {
		body += "\r\n" + msg.Link + "\r\n"
	}

	dial := e.dial
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}

	conn, err := dial(ctx, "tcp", e.Host)
	if err != nil {
		return fmt.Errorf("email: dial: %w", err)
	}

	var client *smtp.Client
	if e.UseTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: strings.Split(e.Host, ":")[0]})
		client, err = smtp.NewClient(tlsConn, e.Host)
	} else {
		client, err = smtp.NewClient(conn, e.Host)
	}
	if err != nil {
		return fmt.Errorf("email: smtp: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok && !e.UseTLS {
		if err := client.StartTLS(&tls.Config{ServerName: strings.Split(e.Host, ":")[0]}); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	if e.Username != "" {
		auth := smtp.PlainAuth("", e.Username, e.Password, strings.Split(e.Host, ":")[0])
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	if err := client.Mail(e.From); err != nil {
		return fmt.Errorf("email: mail from: %w", err)
	}
	if err := client.Rcpt(target); err != nil {
		return fmt.Errorf("email: rcpt: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: data: %w", err)
	}
	if _, err := wc.Write([]byte(body)); err != nil {
		return fmt.Errorf("email: write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("email: close: %w", err)
	}
	return client.Quit()
}
