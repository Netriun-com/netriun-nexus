// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/base64"
	"net/mail"
	"strings"
	"testing"
)

func TestVerificationMessageRejectsLineBreakInjection(t *testing.T) {
	sender := smtpSender{from: mail.Address{Name: "Netriun Nexus", Address: "sender@example.com"}}
	if _, err := sender.verificationMessage("attacker\r\nBcc: injected@example.com", "https://nexus.example.com/verify"); err == nil {
		t.Fatal("untrusted username line break accepted")
	}
	message, err := sender.verificationMessage("normal-user", "https://nexus.example.com/verify?token=a&b=c")
	if err != nil {
		t.Fatal(err)
	}
	text := string(message)
	if !strings.Contains(text, "Content-Transfer-Encoding: base64") {
		t.Fatalf("untrusted body content was not MIME encoded safely: %s", text)
	}
	if strings.Count(text, "Content-Type: text/plain") != 1 || strings.Count(text, "Content-Type: text/html") != 1 {
		t.Fatal("verification message must contain exactly one plain and one HTML part")
	}
}

func TestWriteMIMEBase64WrapsAndRoundTrips(t *testing.T) {
	original := []byte(strings.Repeat("verification-content-", 20))
	var encoded strings.Builder
	if err := writeMIMEBase64(&encoded, original); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(encoded.String()), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("MIME base64 line is %d characters", len(line))
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encoded.String(), "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(original) {
		t.Fatal("base64 body did not round-trip")
	}
}

func TestVerificationMessageRejectsUnsafeURL(t *testing.T) {
	sender := smtpSender{from: mail.Address{Name: "Netriun Nexus", Address: "sender@example.com"}}
	for _, candidate := range []string{"javascript:alert(1)", "https://user:secret@example.com/verify", "https://example.com/verify\x00"} {
		if _, err := sender.verificationMessage("normal-user", candidate); err == nil {
			t.Fatalf("unsafe verification URL accepted: %q", candidate)
		}
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
