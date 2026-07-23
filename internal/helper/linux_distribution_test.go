package helper

import "testing"

func TestLinuxTrustStoreAcceptsOnlyOfficialDistributionFloors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		family  string
		wantErr bool
	}{
		{name: "debian 12", content: "ID=debian\nVERSION_ID=\"12\"\n", family: linuxTrustStoreDebian},
		{name: "ubuntu 24.04", content: "ID=ubuntu\nVERSION_ID=24.04\n", family: linuxTrustStoreDebian},
		{name: "rocky 9", content: "ID=rocky\nVERSION_ID=\"9.4\"\n", family: linuxTrustStoreRHEL},
		{name: "centos stream 9", content: "ID=centos\nVERSION_ID=9\n", family: linuxTrustStoreRHEL},
		{name: "debian 11 is unsupported", content: "ID=debian\nVERSION_ID=11\n", wantErr: true},
		{name: "ubuntu 22 is unsupported", content: "ID=ubuntu\nVERSION_ID=22.04\n", wantErr: true},
		{name: "centos linux 8 is unsupported", content: "ID=centos\nVERSION_ID=8\n", wantErr: true},
		{name: "unknown distro is unsupported", content: "ID=arch\nVERSION_ID=2026\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := linuxTrustStoreFromOSRelease([]byte(tt.content))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("store = %+v, want unsupported distribution error", store)
				}
				return
			}
			if err != nil || store.family != tt.family {
				t.Fatalf("store/err = %+v/%v, want family %q", store, err, tt.family)
			}
		})
	}
}

func TestLinuxTrustStoreUsesFixedFamilySpecificCAPaths(t *testing.T) {
	debian, err := linuxTrustStoreFromOSRelease([]byte("ID=debian\nVERSION_ID=12\n"))
	if err != nil || debian.caPath != "/usr/local/share/ca-certificates/tunnelboard-local-ca.crt" || debian.refreshExecutable != "/usr/sbin/update-ca-certificates" {
		t.Fatalf("debian = %+v, err = %v", debian, err)
	}
	rhel, err := linuxTrustStoreFromOSRelease([]byte("ID=rhel\nVERSION_ID=9.5\n"))
	if err != nil || rhel.caPath != "/etc/pki/ca-trust/source/anchors/tunnelboard-local-ca.crt" || rhel.refreshExecutable != "/usr/bin/update-ca-trust" {
		t.Fatalf("rhel = %+v, err = %v", rhel, err)
	}
}
