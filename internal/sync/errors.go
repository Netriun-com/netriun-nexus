// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"errors"
	"net"
	"strings"

	"golang.org/x/oauth2"
)

var computeErrors = map[string]string{
	"NX-SYNC-AUTH":      "Cloud credentials or permissions were rejected. Check this account's access.",
	"NX-SYNC-TIMEOUT":   "Cloud collection timed out. Nexus will retry.",
	"NX-SYNC-CANCELLED": "Cloud collection was interrupted. Nexus will retry.",
	"NX-SYNC-PROVIDER":  "Cloud provider request failed. Nexus will retry; check provider availability.",
}

// ComputeError classifies SDK codes and local HTTP diagnostics. It never copies
// error messages, URLs, tokens or provider response bodies into persisted state.
func ComputeError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	code := "NX-SYNC-PROVIDER"
	var timeout net.Error
	switch {
	case errors.Is(err, context.Canceled):
		code = "NX-SYNC-CANCELLED"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		code = "NX-SYNC-TIMEOUT"
	default:
		sdkCode := ""
		var aws interface{ ErrorCode() string }
		var ali interface{ GetCode() *string }
		if errors.As(err, &aws) {
			sdkCode = aws.ErrorCode()
		}
		if errors.As(err, &ali) && ali.GetCode() != nil {
			sdkCode = *ali.GetCode()
		}
		for _, prefix := range []string{"AuthFailure", "UnauthorizedOperation", "AccessDenied", "InvalidClientTokenId", "UnrecognizedClientException", "ExpiredToken", "InvalidAccessKeyId", "InvalidAccessKeySecret", "SignatureDoesNotMatch", "Forbidden.RAM", "Forbidden.NoPermission", "InvalidSecurityToken"} {
			if sdkCode == prefix || strings.HasPrefix(sdkCode, prefix+".") {
				code = "NX-SYNC-AUTH"
			}
		}
		// These are exact locally generated diagnostics from the current
		// Azure/GCP clients, not string matching provider response bodies.
		switch err.Error() {
		case "Azure request failed with status 401", "Azure request failed with status 403",
			"Azure authentication failed with status 400", "Azure authentication failed with status 401", "Azure authentication failed with status 403",
			"GCP request failed with status 401", "GCP request failed with status 403":
			code = "NX-SYNC-AUTH"
		}
		var token *oauth2.RetrieveError
		if errors.As(err, &token) && token.Response != nil && (token.Response.StatusCode == 400 || token.Response.StatusCode == 401 || token.Response.StatusCode == 403) {
			code = "NX-SYNC-AUTH"
		}
	}
	return code, computeErrors[code]
}

// SafeStoredError also handles a queue-generated failure (e.g. lease expiry),
// without exposing arbitrary text from a persisted job.
func SafeStoredError(message string) (string, string) {
	for code, safe := range computeErrors {
		if message == safe {
			return code, safe
		}
	}
	return "NX-SYNC-PROVIDER", computeErrors["NX-SYNC-PROVIDER"]
}
