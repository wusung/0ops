package auth

import sharedtoken "github.com/winshare/zeroops/internal/shared/token"

//nolint:revive // exported for public API
type ParsedBearerToken = sharedtoken.ParsedBearerToken

//nolint:revive // exported for public API
func NewBearerToken(kind, id string) (string, error) {
	return sharedtoken.NewBearerToken(kind, id)
}

//nolint:revive // exported for public API
func ParseBearerToken(token string) (ParsedBearerToken, error) {
	return sharedtoken.ParseBearerToken(token)
}

//nolint:revive // exported for public API
func HashBearerToken(secret string) string {
	hash, err := sharedtoken.HashBearerToken(secret)
	if err != nil {
		panic(err)
	}
	return hash
}

//nolint:revive // exported for public API
func CompareBearerToken(secret, encodedHash string) (bool, error) {
	return sharedtoken.CompareBearerToken(secret, encodedHash)
}
