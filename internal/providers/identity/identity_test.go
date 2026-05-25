package identity

import "testing"

func TestProviderIdentityString(t *testing.T) {
	cases := []struct {
		name string
		p    ProviderIdentity
		want string
	}{
		{"empty zero value", ProviderIdentity{}, ""},
		{"missing model", ProviderIdentity{Vendor: "openai"}, ""},
		{"missing vendor", ProviderIdentity{Model: "gpt-4o-mini"}, ""},
		{"summary (no dim)", ProviderIdentity{Vendor: "anthropic", Model: "claude-sonnet-4-6"}, "anthropic:claude-sonnet-4-6"},
		{"embed with dim", ProviderIdentity{Vendor: "openai", Model: "text-embedding-3-large", Dimensions: 1024}, "openai:text-embedding-3-large@1024"},
		{"embed dim zero", ProviderIdentity{Vendor: "ollama", Model: "mxbai-embed-large"}, "ollama:mxbai-embed-large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.p.String()
			if got != c.want {
				t.Fatalf("String() = %q, want %q", got, c.want)
			}
		})
	}
}
