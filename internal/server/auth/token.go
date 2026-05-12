package auth

import sharedtoken "github.com/winshare/zeroops/internal/shared/token"

type ParsedBearerToken = sharedtoken.ParsedBearerToken

func NewBearerToken(kind, id string) (string, error) {
	return sharedtoken.NewBearerToken(kind, id)
}

func ParseBearerToken(token string) (ParsedBearerToken, error) {
	return sharedtoken.ParseBearerToken(token)
}

func HashBearerToken(secret string) string {
	hash, err := sharedtoken.HashBearerToken(secret)
	if err != nil {
		panic(err)
	}
	return hash
}

func CompareBearerToken(secret, encodedHash string) (bool, error) {
	return sharedtoken.CompareBearerToken(secret, encodedHash)
}
