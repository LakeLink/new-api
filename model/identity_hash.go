package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

// crossDatabaseIdentityHash provides a compact, collation-independent key for
// logical identities that would otherwise require oversized compound string
// indexes on MySQL. Length-prefixing keeps distinct part sequences distinct.
func crossDatabaseIdentityHash(parts ...string) string {
	hasher := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hasher.Write(length[:])
		_, _ = hasher.Write([]byte(part))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func normalizeNamedResourceIdentity(name string, maxRunes int) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", errors.New("name is required")
	}
	if maxRunes > 0 && utf8.RuneCountInString(name) > maxRunes {
		return "", "", errors.New("name is too long")
	}
	// Resource names are human-facing identifiers. Case-folding here matches
	// the behavior of common MySQL collations and makes it deterministic on
	// PostgreSQL and SQLite as well.
	return name, crossDatabaseIdentityHash(strings.ToLower(name)), nil
}

func normalizeProviderPaymentIdentity(
	paymentProvider string,
	providerPaymentID string,
) (string, string, string, error) {
	paymentProvider, err := normalizePaymentProvider(paymentProvider)
	if err != nil {
		return "", "", "", err
	}
	if providerPaymentID == "" || !utf8.ValidString(providerPaymentID) ||
		utf8.RuneCountInString(providerPaymentID) > 255 {
		return "", "", "", errors.New("provider payment ID is invalid")
	}
	return paymentProvider,
		providerPaymentID,
		crossDatabaseIdentityHash(paymentProvider, providerPaymentID),
		nil
}

func normalizeProviderSubscriptionIdentity(
	paymentProvider string,
	providerSubscriptionID string,
) (string, string, string, error) {
	paymentProvider, err := normalizePaymentProvider(paymentProvider)
	if err != nil {
		return "", "", "", err
	}
	if providerSubscriptionID == "" || !utf8.ValidString(providerSubscriptionID) ||
		utf8.RuneCountInString(providerSubscriptionID) > 255 {
		return "", "", "", errors.New("provider subscription ID is invalid")
	}
	return paymentProvider,
		providerSubscriptionID,
		crossDatabaseIdentityHash(paymentProvider, providerSubscriptionID),
		nil
}

func normalizePaymentProvider(paymentProvider string) (string, error) {
	paymentProvider = strings.ToLower(strings.TrimSpace(paymentProvider))
	if paymentProvider == "" || utf8.RuneCountInString(paymentProvider) > 50 {
		return "", errors.New("payment provider is invalid")
	}
	return paymentProvider, nil
}

func normalizeTradeNumberIdentity(tradeNo string) (string, string, error) {
	if tradeNo == "" || !utf8.ValidString(tradeNo) ||
		utf8.RuneCountInString(tradeNo) > 255 {
		return "", "", errors.New("trade number is invalid")
	}
	return tradeNo, crossDatabaseIdentityHash(tradeNo), nil
}
