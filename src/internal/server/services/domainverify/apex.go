package domainverify

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// DetectApex reports whether host is the apex of its registrable domain.
// Hard rule § 15 #4: must use publicsuffix; never regex.
func DetectApex(host string) (bool, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return false, errors.New("empty hostname")
	}
	etldPlusOne, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return false, fmt.Errorf("effective TLD+1: %w", err)
	}
	return etldPlusOne == host, nil
}
