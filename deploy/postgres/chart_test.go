package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec § 16 hard rules are translated into substring assertions against the
// raw template files. Helm render-time guards (gt/lt/fail) are exercised by
// the dedicated TestTemplateGuards tests below.
var requiredSubstrings = map[string][]string{
	"Chart.yaml": {
		"name: postgres",
		`appVersion: "17.2"`,
	},
	"values.yaml": {
		// Hard rule #1 — both main and replica replica counts default to 1
		"main:\n  replicas: 1",
		"replica:\n  replicas: 1",
		// Hard rule #3 — archive_timeout ≤ 300s
		"archiveTimeoutSeconds: 300",
		// Spec § 4.4 async commit → RPO 5 min
		`synchronousCommit: "off"`,
		// Hard rule #9 — bucket retention 30 d
		"retentionDays: 30",
		// Spec § 4.4 max_connections
		"maxConnections: 100",
		// Spec § 7.1 — UTC 18:00 daily dump
		`schedule: "0 18 * * *"`,
	},
	"templates/configmap-postgresql-conf.yaml": {
		"archive_command = '/scripts/wal-push.sh %p %f'",
		"archive_mode = {{ .Values.postgresql.archiveMode }}",
		"archive_timeout = {{ .Values.postgresql.archiveTimeoutSeconds }}s",
		"wal_level = {{ .Values.postgresql.walLevel }}",
		"synchronous_commit = {{ .Values.postgresql.synchronousCommit }}",
		"hot_standby = {{ .Values.postgresql.hotStandby }}",
	},
	"templates/statefulset-main.yaml": {
		"kind: StatefulSet",
		// Hard rule #2 — required anti-affinity
		"requiredDuringSchedulingIgnoredDuringExecution",
		"topologyKey: kubernetes.io/hostname",
		// Hard rule #1 floor guard
		"postgres chart requires main.replicas",
		"postgres chart requires replica.replicas",
	},
	"templates/statefulset-replica.yaml": {
		"kind: StatefulSet",
		"requiredDuringSchedulingIgnoredDuringExecution",
		"topologyKey: kubernetes.io/hostname",
		// Spec § 5.1 — pg_basebackup init container
		"name: replica-init",
	},
	"templates/service-main.yaml": {
		"name: postgres-main",
		"port: 5432",
	},
	"templates/service-replica.yaml": {
		"name: postgres-replica",
		"port: 5432",
	},
	"templates/cronjob-pg-dump.yaml": {
		"kind: CronJob",
		// Hard rule #4 — daily dump schedule sourced from values
		"schedule: {{ .Values.pgDump.schedule | quote }}",
		"command: [\"/bin/sh\", \"/scripts/pg-dump.sh\"]",
	},
	"templates/networkpolicy.yaml": {
		"policyTypes:",
		"port: 5432",
	},
	"templates/configmap-scripts.yaml": {
		`.Files.Get "scripts/wal-push.sh"`,
		`.Files.Get "scripts/pg-dump.sh"`,
		`.Files.Get "scripts/replica-init.sh"`,
	},
	"scripts/wal-push.sh": {
		"WAL_S3_BUCKET",
		"WAL_S3_ENDPOINT",
		"aws s3 cp",
	},
	"scripts/pg-dump.sh": {
		"pg_dump -Fc -Z9",
		"DUMP_S3_BUCKET",
	},
	"scripts/replica-init.sh": {
		"pg_basebackup",
		"--write-recovery-conf",
		"standby.signal",
	},
}

func TestChartFilesEnforceHardRules(t *testing.T) {
	for file, substrings := range requiredSubstrings {
		data, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		content := string(data)
		for _, sub := range substrings {
			if !strings.Contains(content, sub) {
				t.Errorf("%s: missing required substring %q", file, sub)
			}
		}
	}
}

// TestTemplateGuards asserts the Helm render-time `fail` calls that catch
// values mis-configurations before they reach a cluster.
func TestTemplateGuards(t *testing.T) {
	cases := []struct {
		file        string
		mustContain []string
	}{
		{
			file: "templates/statefulset-main.yaml",
			mustContain: []string{
				"lt (int .Values.main.replicas) 1",
				"lt (int .Values.replica.replicas) 1",
				`spec § 16 hard rule #1`,
			},
		},
		{
			file: "templates/configmap-postgresql-conf.yaml",
			mustContain: []string{
				"gt (int .Values.postgresql.archiveTimeoutSeconds) 300",
				`spec § 16 hard rule #3`,
			},
		},
	}
	for _, c := range cases {
		data, err := os.ReadFile(filepath.Clean(c.file))
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		content := string(data)
		for _, sub := range c.mustContain {
			if !strings.Contains(content, sub) {
				t.Errorf("%s: missing required guard %q", c.file, sub)
			}
		}
	}
}

// TestRetentionPolicyMatchesHardRule checks both the WAL archive and pg_dump
// retention defaults are 30 days — hard rule #9.
func TestRetentionPolicyMatchesHardRule(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean("values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	content := string(data)
	walIdx := strings.Index(content, "walArchive:")
	if walIdx < 0 {
		t.Fatalf("values.yaml missing walArchive block")
	}
	pgDumpIdx := strings.Index(content, "pgDump:")
	if pgDumpIdx < 0 {
		t.Fatalf("values.yaml missing pgDump block")
	}
	authIdx := strings.Index(content, "auth:")
	if authIdx < 0 {
		t.Fatalf("values.yaml missing auth block")
	}
	walBlock := content[walIdx:pgDumpIdx]
	pgDumpBlock := content[pgDumpIdx:authIdx]
	if !strings.Contains(walBlock, "retentionDays: 30") {
		t.Errorf("walArchive.retentionDays must default to 30 (spec § 16 hard rule #9)")
	}
	if !strings.Contains(pgDumpBlock, "retentionDays: 30") {
		t.Errorf("pgDump.retentionDays must default to 30 (spec § 7.2)")
	}
}
