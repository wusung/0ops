package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/winshare/zeroops/internal/shared/authconfig"
)

type appsContext struct {
	TeamSlug   string
	Host       string
	BearerToken string
}

func resolveAppsContext(teamFlag, hostFlag, tokenFlag string) (appsContext, error) {
	cfg, _ := authconfig.Load()

	host := firstNonEmpty(hostFlag, os.Getenv("OPS_HOST"))
	if host == "" {
		if token, ok := cfg.First(); ok && strings.TrimSpace(token.Host) != "" {
			host = token.Host
		} else {
			host = "http://127.0.0.1:8080"
		}
	}

	token := firstNonEmpty(tokenFlag, os.Getenv("OPS_BEARER_TOKEN"))
	if token == "" {
		if fromFile, ok := cfg.BearerForHost(host); ok {
			token = fromFile
		}
	}

	team := firstNonEmpty(teamFlag, os.Getenv("OPS_TEAM"))
	if team == "" {
		if fromFile, ok := cfg.DefaultTeamForHost(host); ok {
			team = fromFile
		}
	}

	if strings.TrimSpace(team) == "" {
		return appsContext{}, fmt.Errorf("no team in context. run 0ops teams use <slug> or pass --team")
	}
	if strings.TrimSpace(token) == "" {
		return appsContext{}, fmt.Errorf("no bearer token found. run 0ops auth login or pass --token")
	}

	return appsContext{
		TeamSlug:    team,
		Host:        host,
		BearerToken: token,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
