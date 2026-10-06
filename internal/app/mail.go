// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

type mailSender interface {
	SendVerification(to, username, verifyURL string) error
}

type smtpSender struct {
	host, port, username, password string
	from                           mail.Address
}

func newSMTPSender(c runtimeConfig) (*smtpSender, error) {
	from, err := mail.ParseAddress(c.smtpFromAddress)
	if err != nil || !strings.EqualFold(from.Address, c.smtpFromAddress) || containsHeaderBreak(c.smtpFromAddress) || containsHeaderBreak(c.smtpFromName) {
		return nil, fmt.Errorf("SMTP_FROM_ADDRESS must be a plain valid email address")
	}
	from.Name = c.smtpFromName
	return &smtpSender{host: c.smtpHost, port: c.smtpPort, username: c.smtpUsername, password: c.smtpPassword, from: *from}, nil
}

func (s *smtpSender) SendVerification(to, username, verifyURL string) error {
	recipient, err := mail.ParseAddress(to)
	if err != nil || !strings.EqualFold(recipient.Address, to) || containsHeaderBreak(to) {
		return fmt.Errorf("invalid verification recipient")
	}
	message, err := s.verificationMessage(*recipient, username, verifyURL)
	if err != nil {
		return err
	}
	return s.send(recipient.Address, message)
}

func (s *smtpSender) verificationMessage(recipient mail.Address, username, verifyURL string) ([]byte, error) {
	if containsHeaderBreak(username) || containsHeaderBreak(verifyURL) {
		return nil, fmt.Errorf("invalid verification message content")
	}
	subject := "Verify your Netriun Nexus email"
	plain := fmt.Sprintf("Hello %s,\n\nVerify your email to activate your Netriun Nexus workspace:\n%s\n\nThis link expires in 24 hours. If you did not create this account, you can ignore this email.\n", username, verifyURL)
	htmlBody := fmt.Sprintf("<p>Hello %s,</p><p>Verify your email to activate your Netriun Nexus workspace:</p><p><a href=\"%s\">Verify email address</a></p><p>This link expires in 24 hours. If you did not create this account, you can ignore this email.</p>", html.EscapeString(username), html.EscapeString(verifyURL))
	boundaryBytes := make([]byte, 18)
	if _, err := rand.Read(boundaryBytes); err != nil {
		return nil, err
	}
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	if err := multipartWriter.SetBoundary("nexus-" + base64.RawURLEncoding.EncodeToString(boundaryBytes)); err != nil {
		return nil, err
	}
	for contentType, content := range map[string]string{
		"text/plain; charset=UTF-8": plain,
		"text/html; charset=UTF-8":  htmlBody,
	} {
		part, err := multipartWriter.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		quoted := quotedprintable.NewWriter(part)
		if _, err = quoted.Write([]byte(content)); err != nil {
			return nil, err
		}
		if err = quoted.Close(); err != nil {
			return nil, err
		}
	}
	if err := multipartWriter.Close(); err != nil {
		return nil, err
	}
	var message bytes.Buffer
	for _, header := range []string{
		"From: " + s.from.String(),
		"To: " + recipient.String(),
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=" + multipartWriter.Boundary(),
	} {
		if containsHeaderBreak(header) {
			return nil, fmt.Errorf("invalid email header")
		}
		message.WriteString(header + "\r\n")
	}
	message.WriteString("\r\n")
	message.Write(body.Bytes())
	return message.Bytes(), nil
}

func containsHeaderBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func (s *smtpSender) send(to string, message []byte) error {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.Dial("tcp", net.JoinHostPort(s.host, s.port))
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		connection.Close()
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return fmt.Errorf("SMTP server does not offer STARTTLS")
	}
	if err = client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}
	if err = client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
		return err
	}
	if err = client.Mail(s.from.Address); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		writer.Close()
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func verificationURL(origin, token string) string {
	return origin + "/verify-email?token=" + url.QueryEscape(token)
}
