package updater

import "testing"

func TestIsRemoteVersionNewerSupportsFourPartReleaseVersions(t *testing.T) {
	tests := []struct {
		name    string
		current string
		remote  string
		want    bool
	}{
		{name: "fourth segment increases", current: "v1.0.5.2", remote: "v1.0.5.3", want: true},
		{name: "fourth segment decreases", current: "v1.0.5.3", remote: "v1.0.5.2", want: false},
		{name: "three-part next patch", current: "v1.0.5.3", remote: "v1.0.6", want: true},
		{name: "release is newer than prerelease", current: "v1.0.5.3-alpha", remote: "v1.0.5.3", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRemoteVersionNewer(tt.current, tt.remote); got != tt.want {
				t.Fatalf("isRemoteVersionNewer(%q, %q) = %t, want %t", tt.current, tt.remote, got, tt.want)
			}
		})
	}
}
