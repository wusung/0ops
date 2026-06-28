package security

import "testing"

func TestEvaluateAnomaly(t *testing.T) {
	def := DefaultAnomalyThresholds()

	t.Run("sustained rate limiting trips detection", func(t *testing.T) {
		dec := EvaluateAnomaly(AnomalySignals{SustainedRateLimit: true}, def)
		if !dec.Detected {
			t.Fatal("expected detection on sustained rate limiting")
		}
		if dec.AuditAction != AbuseDetectedAction {
			t.Fatalf("audit action = %q, want %q", dec.AuditAction, AbuseDetectedAction)
		}
		if dec.SubjectType != AnomalySubjectType {
			t.Fatalf("subject type = %q, want %q", dec.SubjectType, AnomalySubjectType)
		}
	})

	t.Run("high forbidden ratio trips detection", func(t *testing.T) {
		dec := EvaluateAnomaly(AnomalySignals{ForbiddenWriteRatio: 0.9}, def)
		if !dec.Detected {
			t.Fatal("expected detection on high forbidden-write ratio")
		}
	})

	t.Run("below thresholds does not trip", func(t *testing.T) {
		dec := EvaluateAnomaly(AnomalySignals{ForbiddenWriteRatio: 0.1}, def)
		if dec.Detected {
			t.Fatal("expected no detection below thresholds")
		}
		if dec.AuditAction != "" {
			t.Fatalf("expected empty audit action when not detected, got %q", dec.AuditAction)
		}
	})

	t.Run("v1 reaction is alert-only: never auto-throttle or re-auth", func(t *testing.T) {
		dec := EvaluateAnomaly(AnomalySignals{SustainedRateLimit: true, ForbiddenWriteRatio: 1.0}, def)
		if dec.AutoThrottle {
			t.Fatal("v1 must not auto-throttle (rate-limit-and-abuse hard rule #7)")
		}
		if dec.RequireReauth {
			t.Fatal("v1 must not require re-auth")
		}
	})
}

func TestAnomalyAuditEntry(t *testing.T) {
	dec := EvaluateAnomaly(AnomalySignals{SustainedRateLimit: true}, DefaultAnomalyThresholds())
	entry := AnomalyAuditEntry("team-1", "tok-9", dec)

	if entry.Action != AbuseDetectedAction {
		t.Fatalf("entry action = %q, want %q", entry.Action, AbuseDetectedAction)
	}
	if entry.SubjectType != AnomalySubjectType {
		t.Fatalf("entry subject type = %q, want %q", entry.SubjectType, AnomalySubjectType)
	}
	if entry.SubjectID == nil || *entry.SubjectID != "tok-9" {
		t.Fatalf("entry subject id = %v, want tok-9", entry.SubjectID)
	}
	if entry.TeamID != "team-1" {
		t.Fatalf("entry team id = %q, want team-1", entry.TeamID)
	}
	// spec §6.2: actor=system:anomaly is realised as Source=system with a nil
	// user actor (audit_log.actor_user_id is a user FK; no fabricated UUID).
	if string(entry.Source) != "system" {
		t.Fatalf("entry source = %q, want system", entry.Source)
	}
	if entry.ActorUserID != nil {
		t.Fatalf("entry actor user id = %v, want nil", entry.ActorUserID)
	}
}

func TestAnomalyEntryNotEmittedWhenNotDetected(t *testing.T) {
	dec := EvaluateAnomaly(AnomalySignals{}, DefaultAnomalyThresholds())
	if dec.Detected {
		t.Fatal("expected no detection for empty signals")
	}
}
