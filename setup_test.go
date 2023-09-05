package corednstailscale

import (
	"testing"
	"time"

	"github.com/coredns/caddy"
	"github.com/google/go-cmp/cmp"
)

func TestParseConfig(t *testing.T) {
	for tn, tc := range map[string]struct {
		input   string
		want    Config
		wantErr bool
	}{
		// Pathological cases
		"empty": {
			wantErr: true,
		},
		"repeated reload": {
			input: `tailscale corp.example.com. {
				reload 1s
				reload 2s
			}`,
			wantErr: true,
		},
		"repeated tag": {
			input: `tailscale corp.example.com. {
				tag foo foo.corp.example.com.
				tag foo bar.corp.example.com.
			}`,
			wantErr: true,
		},
		"unknown option": {
			input: `tailscale corp.example.com. {
				foo bar
			}`,
			wantErr: true,
		},
		"full block but no default zone": {
			input: `tailscale {
				reload 300s
				tag campus-den den.corp.example.com.
				tag campus-rdu rdu.corp.example.com.
				tag prod example.com.
			}`,
			wantErr: true,
		},

		// Sane cases
		"default zone only": {
			input: "tailscale corp.example.com.",
			want: Config{
				DefaultZone:    "corp.example.com.",
				ReloadInterval: defaultReloadInterval,
			},
		},
		"empty block": {
			input: `tailscale corp.example.com. {
				}`,
			want: Config{
				DefaultZone:    "corp.example.com.",
				ReloadInterval: defaultReloadInterval,
			},
		},
		"full example": {
			input: `tailscale corp.example.com. {
				reload 300s
				tag campus-den den.corp.example.com.
				tag campus-rdu rdu.corp.example.com.
				tag prod example.com.
			}`,
			want: Config{
				DefaultZone:    "corp.example.com.",
				ReloadInterval: 300 * time.Second,
				Zones: map[string]string{
					"campus-den": "den.corp.example.com.",
					"campus-rdu": "rdu.corp.example.com.",
					"prod":       "example.com.",
				},
			},
		},
	} {
		t.Run(tn, func(t *testing.T) {
			var got Config
			if err := parse(caddy.NewTestController("dns", tc.input), &got); (err != nil) != tc.wantErr {
				t.Errorf("unexpected error value: %v", err)
			}
			if tc.wantErr {
				// Do not compare Config values when parse returns an error.
				return
			}
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("mismatch: (-got,+want):\n%v", diff)
			}
		})
	}
}
