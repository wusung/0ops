package domainverify

import (
	"context"
	"errors"
	"testing"
)

type fakeResolver struct {
	cname    map[string]string
	hosts    map[string][]string
	txt      map[string][]string
	cnameErr map[string]error
	hostsErr map[string]error
	txtErr   map[string]error
}

func (f *fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if err, ok := f.cnameErr[host]; ok {
		return "", err
	}
	return f.cname[host], nil
}

func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if err, ok := f.hostsErr[host]; ok {
		return nil, err
	}
	return f.hosts[host], nil
}

func (f *fakeResolver) LookupTXT(_ context.Context, host string) ([]string, error) {
	if err, ok := f.txtErr[host]; ok {
		return nil, err
	}
	return f.txt[host], nil
}

func TestDualConditionPassesNonApex(t *testing.T) {
	t.Parallel()
	r := &fakeResolver{
		cname: map[string]string{
			"app.example.com": "tunnel-abc.cfargotunnel.com.",
		},
		txt: map[string][]string{
			"_0ops-verify.app.example.com": {"tok-value"},
		},
	}
	err := DualCondition(context.Background(), r, VerifyInput{
		Hostname:     "app.example.com",
		IsApex:       false,
		Token:        "tok-value",
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestDualConditionRejectsTXTMissing(t *testing.T) {
	t.Parallel()
	r := &fakeResolver{
		cname: map[string]string{
			"app.example.com": "tunnel-abc.cfargotunnel.com.",
		},
		txt: map[string][]string{
			"_0ops-verify.app.example.com": {"other-token"},
		},
	}
	err := DualCondition(context.Background(), r, VerifyInput{
		Hostname:     "app.example.com",
		IsApex:       false,
		Token:        "tok-value",
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if !errors.Is(err, ErrTXTNotMatched) {
		t.Fatalf("got %v, want ErrTXTNotMatched", err)
	}
}

func TestDualConditionRejectsCNAMEMissing(t *testing.T) {
	t.Parallel()
	r := &fakeResolver{
		cname: map[string]string{
			"app.example.com": "elsewhere.example.net.",
		},
		txt: map[string][]string{
			"_0ops-verify.app.example.com": {"tok-value"},
		},
	}
	err := DualCondition(context.Background(), r, VerifyInput{
		Hostname:     "app.example.com",
		IsApex:       false,
		Token:        "tok-value",
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if !errors.Is(err, ErrCNAMENotMatched) {
		t.Fatalf("got %v, want ErrCNAMENotMatched", err)
	}
}

func TestDualConditionPassesApex(t *testing.T) {
	t.Parallel()
	r := &fakeResolver{
		hosts: map[string][]string{
			"example.com":                 {"104.16.1.1", "104.16.2.2"},
			"tunnel-abc.cfargotunnel.com": {"104.16.1.1", "104.16.2.2"},
		},
		txt: map[string][]string{
			"_0ops-verify.example.com": {"tok-value"},
		},
	}
	err := DualCondition(context.Background(), r, VerifyInput{
		Hostname:     "example.com",
		IsApex:       true,
		Token:        "tok-value",
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestDualConditionRejectsApexHostMismatch(t *testing.T) {
	t.Parallel()
	r := &fakeResolver{
		hosts: map[string][]string{
			"example.com":                 {"203.0.113.1"},
			"tunnel-abc.cfargotunnel.com": {"104.16.1.1"},
		},
		txt: map[string][]string{
			"_0ops-verify.example.com": {"tok-value"},
		},
	}
	err := DualCondition(context.Background(), r, VerifyInput{
		Hostname:     "example.com",
		IsApex:       true,
		Token:        "tok-value",
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if !errors.Is(err, ErrCNAMENotMatched) {
		t.Fatalf("got %v, want ErrCNAMENotMatched", err)
	}
}

func TestDualConditionWrapsLookupError(t *testing.T) {
	t.Parallel()
	boom := errors.New("network down")
	r := &fakeResolver{
		cnameErr: map[string]error{"app.example.com": boom},
	}
	err := DualCondition(context.Background(), r, VerifyInput{
		Hostname:     "app.example.com",
		IsApex:       false,
		Token:        "tok-value",
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
