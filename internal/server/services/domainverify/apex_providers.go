package domainverify

// ApexProvider describes a DNS provider known to be incompatible with apex
// CNAME flattening / ALIAS / ANAME. Source: spec § 5.3.
type ApexProvider struct {
	Name        string
	Reason      string
	Alternative string
}

// IncompatibleApexProviders returns the static set of known-incompatible
// providers. Hard rule § 15 #4 mandates `publicsuffix`; this list is only
// for UX guidance in the side_effects payload.
func IncompatibleApexProviders() []ApexProvider {
	return []ApexProvider{
		{
			Name:        "GoDaddy (classic DNS)",
			Reason:      "does not support ALIAS / ANAME records",
			Alternative: "migrate to Cloudflare DNS, or use a non-apex subdomain (e.g. www.<domain>)",
		},
		{
			Name:        "Microsoft Azure DNS (classic)",
			Reason:      "does not support ALIAS / ANAME records",
			Alternative: "migrate DNS to Cloudflare, or delegate the apex via NS",
		},
		{
			Name:        "Legacy self-hosted BIND (no CNAME-on-apex extension)",
			Reason:      "RFC 1034 prohibits CNAME-on-apex; vanilla BIND has no flattening",
			Alternative: "use a non-apex subdomain, or move the zone to a flattening-capable provider",
		},
	}
}
