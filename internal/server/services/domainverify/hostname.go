package domainverify

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidHostname is returned when a hostname fails RFC 1035 / length checks.
var ErrInvalidHostname = errors.New("invalid hostname")

// ErrReservedHostname is returned when a hostname uses a reserved suffix.
var ErrReservedHostname = errors.New("reserved hostname")

const reservedSuffix = ".winshare.tw"

// ValidateHostname enforces hard rule § 15 #3: reject reserved suffix;
// applies RFC 1035 + length limits required by spec § 5.2.
func ValidateHostname(host string) error {
	if host == "" {
		return fmt.Errorf("%w: empty", ErrInvalidHostname)
	}
	if host != strings.ToLower(host) {
		return fmt.Errorf("%w: must be lowercase", ErrInvalidHostname)
	}
	if strings.HasSuffix(host, ".") {
		return fmt.Errorf("%w: trailing dot", ErrInvalidHostname)
	}
	if len(host) > 253 {
		return fmt.Errorf("%w: length > 253", ErrInvalidHostname)
	}
	reservedApex := strings.TrimPrefix(reservedSuffix, ".")
	if strings.HasSuffix(host, reservedSuffix) || host == reservedApex {
		return fmt.Errorf("%w: %s suffix is reserved", ErrReservedHostname, reservedSuffix)
	}

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return fmt.Errorf("%w: must contain at least one dot", ErrInvalidHostname)
	}
	for _, label := range labels {
		if err := validateLabel(label); err != nil {
			return err
		}
	}
	return nil
}

func validateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("%w: empty label", ErrInvalidHostname)
	}
	if len(label) > 63 {
		return fmt.Errorf("%w: label > 63 chars", ErrInvalidHostname)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("%w: label cannot start/end with '-'", ErrInvalidHostname)
	}
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("%w: label contains invalid char %q", ErrInvalidHostname, r)
		}
	}
	return nil
}

func isReservedSuffixErr(err error) bool {
	return errors.Is(err, ErrReservedHostname)
}
