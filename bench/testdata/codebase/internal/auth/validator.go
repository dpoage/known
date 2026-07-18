package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"pipeliner/internal/config"
	perrors "pipeliner/internal/errors"
)

// Validator checks API keys and request signatures for the webhook output.
// Despite the package name "auth" and the type name "Validator", this does
// NOT validate pipeline data — it validates outbound webhook credentials.
// Pipeline data validation is in internal/pipeline/validate.go.
type Validator struct {
	cfg *config.AuthConfig
}

// NewValidator creates an auth validator from the auth config section.
func NewValidator(cfg *config.AuthConfig) *Validator {
	return &Validator{cfg: cfg}
}

// ValidateAPIKey checks whether the given key matches the configured API key.
func (v *Validator) ValidateAPIKey(key string) error {
	if v.cfg.APIKey == "" {
		return perrors.NewAuthError("no API key configured")
	}

	if !secureCompare(key, v.cfg.APIKey) {
		return perrors.NewAuthError("invalid API key")
	}

	return nil
}

// ValidateIP checks if the source IP is in the allowed list.
func (v *Validator) ValidateIP(ip string) error {
	if len(v.cfg.AllowedIPs) == 0 {
		// No IP restriction configured; allow all.
		return nil
	}

	// Normalize IPv6-mapped IPv4 addresses.
	ip = normalizeIP(ip)

	for _, allowed := range v.cfg.AllowedIPs {
		if normalizeIP(allowed) == ip {
			return nil
		}
	}

	return perrors.NewAuthError(fmt.Sprintf("IP %s not in allowed list", ip))
}

// SignPayload creates an HMAC-SHA256 signature for webhook payloads.
// The signature includes a timestamp to prevent replay attacks.
func (v *Validator) SignPayload(payload []byte) (string, string) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	message := timestamp + "." + string(payload)

	mac := hmac.New(sha256.New, []byte(v.cfg.APIKey))
	mac.Write([]byte(message))
	signature := hex.EncodeToString(mac.Sum(nil))

	return signature, timestamp
}

// VerifySignature checks an incoming webhook callback signature.
func (v *Validator) VerifySignature(payload []byte, signature, timestamp string) error {
	message := timestamp + "." + string(payload)

	mac := hmac.New(sha256.New, []byte(v.cfg.APIKey))
	mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !secureCompare(signature, expected) {
		return perrors.NewAuthError("invalid signature")
	}

	return nil
}

// secureCompare does constant-time string comparison to prevent timing attacks.
func secureCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return hmac.Equal([]byte(a), []byte(b))
}

// normalizeIP strips port suffixes and IPv6 prefixes from IP strings.
func normalizeIP(ip string) string {
	// Strip port if present (e.g., "192.168.1.1:8080").
	if idx := strings.LastIndex(ip, ":"); idx > 0 {
		// Check if this is IPv6 or IPv4 with port.
		if strings.Count(ip, ":") == 1 {
			ip = ip[:idx]
		}
	}

	// Strip IPv6-mapped IPv4 prefix.
	ip = strings.TrimPrefix(ip, "::ffff:")

	return ip
}
