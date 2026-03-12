package sqlite

import "testing"

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain words", input: "simple query", want: "simple query"},
		{name: "hyphenated term", input: "oauth2-proxy", want: `"oauth2-proxy"`},
		{name: "colon term", input: "foo:bar", want: `"foo:bar"`},
		{name: "mixed plain and special", input: "keycloak oauth2-proxy config", want: `keycloak "oauth2-proxy" config`},
		{name: "prefix wildcard preserved", input: "auth*", want: "auth*"},
		{name: "already quoted", input: `"already quoted"`, want: `"already quoted"`},
		{name: "plus operator", input: "+required", want: `"+required"`},
		{name: "empty string", input: "", want: ""},
		{name: "single word", input: "hello", want: "hello"},
		{name: "tilde", input: "near~3", want: `"near~3"`},
		{name: "parentheses", input: "(group)", want: `"(group)"`},
		{name: "multiple hyphens", input: "my-cool-thing other-thing", want: `"my-cool-thing" "other-thing"`},
		{name: "hyphen with prefix wildcard", input: "oauth2-proxy*", want: `"oauth2-proxy"*`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
