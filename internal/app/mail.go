// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime/multipart"
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
	if containsUnsafeMailText(username) || len(username) > 100 {
		return nil, fmt.Errorf("invalid verification message content")
	}
	verificationURI, err := url.Parse(verifyURL)
	if err != nil || (verificationURI.Scheme != "https" && verificationURI.Scheme != "http") || verificationURI.Host == "" || verificationURI.User != nil || len(verifyURL) > 2048 || containsUnsafeMailText(verifyURL) {
		return nil, fmt.Errorf("invalid verification URL")
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
	for _, item := range []struct{ contentType, content string }{
		{"text/plain; charset=UTF-8", plain},
		{"text/html; charset=UTF-8", htmlBody},
	} {
		part, err := multipartWriter.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {item.contentType},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, err
		}
		if err = writeMIMEBase64(part, []byte(item.content)); err != nil {
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

func containsUnsafeMailText(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return r == '\r' || r == '\n' || r == 0 || (r < 0x20 && r != '\t') || (r >= 0x7f && r <= 0x9f)
	}) >= 0
}

func writeMIMEBase64(destination io.Writer, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	for len(encoded) > 76 {
		if _, err := io.WriteString(destination, encoded[:76]+"\r\n"); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	_, err := io.WriteString(destination, encoded+"\r\n")
	return err
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
