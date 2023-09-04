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
		"empty": {
			wantErr: true,
		},
		"repeated reload": {
			input: `tailscale corp.example.com. {
				reload 1s
				reload 2s
			}`,
			want: Config{
				DefaultZone:    "corp.example.com.",
				ReloadInterval: time.Second, // First time is used.
			},
			wantErr: true,
		},
		"repeated tag": {
			input: `tailscale corp.example.com. {
				tag foo foo.corp.example.com.
				tag foo bar.corp.example.com.
			}`,
			want: Config{
				DefaultZone: "corp.example.com.",
				Zones: map[string]string{
					"foo": "foo.corp.example.com.",
				},
			},
			wantErr: true,
		},
		"default zone only": {
			input: "tailscale corp.example.com.",
			want: Config{
				DefaultZone: "corp.example.com.",
			},
		},
		"empty block": {
			input: `tailscale corp.example.com. {
				}`,
			want: Config{
				DefaultZone: "corp.example.com.",
			},
		},
		"unknown option": {
			input: `tailscale corp.example.com. {
				foo bar
			}`,
			want: Config{
				DefaultZone: "corp.example.com.",
			},
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
			got, err := parse(caddy.NewTestController("dns", tc.input))
			if (err != nil) != tc.wantErr {
				t.Errorf("unexpected error value: %v", err)
			}
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("mismatch: (-got,+want):\n%v", diff)
			}
		})
	}
}
