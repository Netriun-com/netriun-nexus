// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"net/mail"
	"strings"
	"testing"
)

func TestVerificationMessageRejectsLineBreakInjection(t *testing.T) {
	sender := smtpSender{from: mail.Address{Name: "Netriun Nexus", Address: "sender@example.com"}}
	if _, err := sender.verificationMessage(mail.Address{Address: "user@example.com"}, "attacker\r\nBcc: injected@example.com", "https://nexus.example.com/verify"); err == nil {
		t.Fatal("untrusted username line break accepted")
	}
	message, err := sender.verificationMessage(mail.Address{Address: "user@example.com"}, "normal-user", "https://nexus.example.com/verify?token=a&b=c")
	if err != nil {
		t.Fatal(err)
	}
	text := string(message)
	if !strings.Contains(text, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("untrusted body content was not MIME encoded safely: %s", text)
	}
	if strings.Count(text, "Content-Type: text/plain") != 1 || strings.Count(text, "Content-Type: text/html") != 1 {
		t.Fatal("verification message must contain exactly one plain and one HTML part")
	}
}

func TestSMTPSenderRejectsHeaderBreaks(t *testing.T) {
	config := runtimeConfig{smtpFromAddress: "sender@example.com", smtpFromName: "Netriun\r\nBcc: attacker@example.com"}
	if _, err := newSMTPSender(config); err == nil {
		t.Fatal("SMTP display-name header injection accepted")
	}
	sender := smtpSender{from: mail.Address{Name: "Netriun Nexus", Address: "sender@example.com"}}
	if err := sender.SendVerification("victim@example.com\r\nBcc: attacker@example.com", "user", "https://example.com"); err == nil {
		t.Fatal("recipient header injection accepted")
	}
}
