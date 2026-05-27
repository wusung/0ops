package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/winshare/zeroops/internal/shared/authconfig"
)

type appsContext struct {
	TeamSlug    string
	Host        string
	BearerToken string
}

func resolveAppsContext(teamFlag, hostFlag, tokenFlag string) (appsContext, error) {
	base, err := resolveBackendContext(hostFlag, tokenFlag)
	if err != nil {
		return appsContext{}, err
	}

	cfg, _ := authconfig.Load()

	team := firstNonEmpty(teamFlag, os.Getenv("OPS_TEAM"))
	if team == "" {
		if fromFile, ok := cfg.DefaultTeamForHost(base.Host); ok {
			team = fromFile
		}
	}

	if strings.TrimSpace(team) == "" {
		return appsContext{}, fmt.Errorf("no team in context. run 0ops teams use <slug> or pass --team")
	}

	return appsContext{
		TeamSlug:    team,
		Host:        base.Host,
		BearerToken: base.BearerToken,
	}, nil
}

type backendContext struct {
	Host        string
	BearerToken string
}

func resolveBackendContext(hostFlag, tokenFlag string) (backendContext, error) {
	cfg, _ := authconfig.Load()

	host := resolveHost(hostFlag, cfg)
	token := firstNonEmpty(tokenFlag, os.Getenv("OPS_BEARER_TOKEN"))
	if token == "" {
		if fromFile, ok := cfg.BearerForHost(host); ok {
			token = fromFile
		}
	}
	if strings.TrimSpace(token) == "" {
		return backendContext{}, fmt.Errorf("no bearer token found. run 0ops auth login or pass --token")
	}

	return backendContext{
		Host:        host,
		BearerToken: token,
	}, nil
}

func resolveHost(hostFlag string, cfg authconfig.File) string {
	host := firstNonEmpty(hostFlag, os.Getenv("OPS_HOST"))
	if host != "" {
		return host
	}
	if token, ok := cfg.First(); ok && strings.TrimSpace(token.Host) != "" {
		return token.Host
	}
	if port := firstNonEmpty(os.Getenv("OPS_HOST_PORT"), readDotEnv("OPS_HOST_PORT")); port != "" {
		return "http://127.0.0.1:" + port
	}
	return "http://127.0.0.1:8080"
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

func readDotEnv(key string) string {
	path := filepath.Join(".", ".env") //nolint:gosec // Path is constructed safely
	data, err := os.ReadFile(path)     //nolint:gosec // Path is from constant
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		return v
	}
	return ""
}
