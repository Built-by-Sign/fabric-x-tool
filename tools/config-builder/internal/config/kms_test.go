package config

import (
	"reflect"
	"testing"
)

func TestResolvePeerOrgKMSUserPinPrefersOrgLevel(t *testing.T) {
	org := &PeerOrg{
		Name:       "Org1MSP",
		KMSUserPin: "org-pin",
		Peers:      []Node{{Name: "peer0", UserPin: "node-pin"}},
	}
	got, err := org.ResolveKMSUserPin()
	if err != nil {
		t.Fatalf("ResolveKMSUserPin: %v", err)
	}
	if got != "org-pin" {
		t.Fatalf("want org-pin, got %q", got)
	}
}

func TestResolvePeerOrgKMSUserPinFallsBackToFirstPeer(t *testing.T) {
	org := &PeerOrg{
		Name:  "Org1MSP",
		Peers: []Node{{Name: "peer0", UserPin: "node-pin"}},
	}
	got, err := org.ResolveKMSUserPin()
	if err != nil {
		t.Fatalf("ResolveKMSUserPin: %v", err)
	}
	if got != "node-pin" {
		t.Fatalf("want node-pin, got %q", got)
	}
}

func TestResolvePeerOrgKMSUserPinErrorsWhenMissing(t *testing.T) {
	org := &PeerOrg{Name: "Org1MSP", Peers: []Node{{Name: "peer0"}}}
	if _, err := org.ResolveKMSUserPin(); err == nil {
		t.Fatal("expected error when no PIN configured")
	}
}

func TestResolveMSPCAURLPrefersOrgOverride(t *testing.T) {
	tests := []struct {
		name      string
		orgURL    string
		globalURL string
		want      string
		wantErr   bool
	}{
		{"org override wins", "http://org-ca:7054", "http://global-ca:7054", "http://org-ca:7054", false},
		{"fallback to global", "", "http://global-ca:7054", "http://global-ca:7054", false},
		{"missing both errors", "", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			peer := &PeerOrg{Name: "Org1MSP", CAURL: tc.orgURL}
			got, err := peer.ResolveMSPCAURL(tc.globalURL)
			if (err != nil) != tc.wantErr {
				t.Fatalf("PeerOrg err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("PeerOrg got %q want %q", got, tc.want)
			}

			orderer := &OrdererOrg{Name: "OrdererOrg1", CAURL: tc.orgURL}
			got, err = orderer.ResolveMSPCAURL(tc.globalURL)
			if (err != nil) != tc.wantErr {
				t.Fatalf("OrdererOrg err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("OrdererOrg got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveTLSCAURLFallsBackToMSPCAURL(t *testing.T) {
	tests := []struct {
		name      string
		orgCA     string
		orgTLSCA  string
		globalURL string
		want      string
	}{
		{"explicit tls url wins", "http://msp:7054", "http://tls:7054", "", "http://tls:7054"},
		{"falls back to org msp url", "http://msp:7054", "", "", "http://msp:7054"},
		{"falls back to global", "", "", "http://global:7054", "http://global:7054"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			peer := &PeerOrg{Name: "Org1MSP", CAURL: tc.orgCA, TLSCAURL: tc.orgTLSCA}
			got, err := peer.ResolveTLSCAURL(tc.globalURL)
			if err != nil {
				t.Fatalf("PeerOrg.ResolveTLSCAURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("PeerOrg got %q want %q", got, tc.want)
			}

			orderer := &OrdererOrg{Name: "OrdererOrg1", CAURL: tc.orgCA, TLSCAURL: tc.orgTLSCA}
			got, err = orderer.ResolveTLSCAURL(tc.globalURL)
			if err != nil {
				t.Fatalf("OrdererOrg.ResolveTLSCAURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("OrdererOrg got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveTLSCAURLErrorsWhenNothingSet(t *testing.T) {
	peer := &PeerOrg{Name: "Org1MSP"}
	if _, err := peer.ResolveTLSCAURL(""); err == nil {
		t.Fatal("expected error when no CA URL configured for peer org")
	}
	orderer := &OrdererOrg{Name: "OrdererOrg1"}
	if _, err := orderer.ResolveTLSCAURL(""); err == nil {
		t.Fatal("expected error when no CA URL configured for orderer org")
	}
}

func TestResolveSetupPinPrefersExplicitSetupPin(t *testing.T) {
	peer := &PeerOrg{
		Name:        "Org1MSP",
		KMSUserPin:  "runtime-pin",
		KMSSetupPin: "setup-pin",
		Peers:       []Node{{Name: "peer0"}},
	}
	got, err := peer.ResolveSetupPin()
	if err != nil {
		t.Fatalf("ResolveSetupPin: %v", err)
	}
	if got != "setup-pin" {
		t.Fatalf("PeerOrg got %q want setup-pin", got)
	}

	orderer := &OrdererOrg{
		Name:        "OrdererOrg1",
		KMSUserPin:  "runtime-pin",
		KMSSetupPin: "setup-pin",
		Orderers:    []Node{{Name: "orderer0"}},
	}
	got, err = orderer.ResolveSetupPin()
	if err != nil {
		t.Fatalf("ResolveSetupPin: %v", err)
	}
	if got != "setup-pin" {
		t.Fatalf("OrdererOrg got %q want setup-pin", got)
	}
}

func TestResolveSetupPinFallsBackToRuntimePin(t *testing.T) {
	peer := &PeerOrg{
		Name:       "Org1MSP",
		KMSUserPin: "runtime-pin",
		Peers:      []Node{{Name: "peer0"}},
	}
	got, err := peer.ResolveSetupPin()
	if err != nil {
		t.Fatalf("ResolveSetupPin: %v", err)
	}
	if got != "runtime-pin" {
		t.Fatalf("PeerOrg got %q want runtime-pin", got)
	}

	orderer := &OrdererOrg{
		Name:       "OrdererOrg1",
		KMSUserPin: "runtime-pin",
		Orderers:   []Node{{Name: "orderer0"}},
	}
	got, err = orderer.ResolveSetupPin()
	if err != nil {
		t.Fatalf("ResolveSetupPin: %v", err)
	}
	if got != "runtime-pin" {
		t.Fatalf("OrdererOrg got %q want runtime-pin", got)
	}
}

func TestResolveSetupPinErrorsWhenAllMissing(t *testing.T) {
	peer := &PeerOrg{Name: "Org1MSP", Peers: []Node{{Name: "peer0"}}}
	if _, err := peer.ResolveSetupPin(); err == nil {
		t.Fatal("expected error when no PIN configured for peer org")
	}
	orderer := &OrdererOrg{Name: "OrdererOrg1", Orderers: []Node{{Name: "orderer0"}}}
	if _, err := orderer.ResolveSetupPin(); err == nil {
		t.Fatal("expected error when no PIN configured for orderer org")
	}
}

func TestResolveTLSHosts(t *testing.T) {
	defaults := []string{"host.docker.internal", "localhost", "127.0.0.1"}

	tests := []struct {
		name        string
		defaults    []string
		orgTLSHosts []string
		node        *Node
		domain      string
		want        []string
	}{
		{
			name:     "nil node returns nil",
			defaults: defaults,
			node:     nil,
			want:     nil,
		},
		{
			name:     "fqdn first, then host, then defaults",
			defaults: defaults,
			node:     &Node{Name: "peer0", Host: "peer0-host"},
			domain:   "org1.example.com",
			want: []string{
				"peer0.org1.example.com",
				"peer0-host",
				"host.docker.internal",
				"localhost",
				"127.0.0.1",
			},
		},
		{
			name:        "org hosts appended after defaults",
			defaults:    defaults,
			orgTLSHosts: []string{"lb.example.com", "10.0.1.10"},
			node:        &Node{Name: "peer0", Host: "peer0-host"},
			domain:      "org1.example.com",
			want: []string{
				"peer0.org1.example.com",
				"peer0-host",
				"host.docker.internal",
				"localhost",
				"127.0.0.1",
				"lb.example.com",
				"10.0.1.10",
			},
		},
		{
			name:        "dedup preserves first occurrence",
			defaults:    []string{"host.docker.internal", "localhost"},
			orgTLSHosts: []string{"host.docker.internal", "lb.example.com"},
			node:        &Node{Name: "peer0", Host: "host.docker.internal"},
			domain:      "org1.example.com",
			want: []string{
				"peer0.org1.example.com",
				"host.docker.internal",
				"localhost",
				"lb.example.com",
			},
		},
		{
			name:     "empty entries dropped",
			defaults: []string{"", "localhost", ""},
			node:     &Node{Name: "peer0"},
			domain:   "org1.example.com",
			want:     []string{"peer0.org1.example.com", "localhost"},
		},
		{
			name:     "missing domain skips fqdn",
			defaults: defaults,
			node:     &Node{Name: "peer0", Host: "peer0-host"},
			domain:   "",
			want:     []string{"peer0-host", "host.docker.internal", "localhost", "127.0.0.1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveTLSHosts(tc.defaults, tc.orgTLSHosts, tc.node, tc.domain)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestResolveKMSTokenLabel(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *NetworkConfig
		orgLabel string
		want     string
		wantErr  bool
	}{
		{
			name:     "global wins",
			cfg:      &NetworkConfig{KMS: &KMSConfig{TokenLabel: "global"}},
			orgLabel: "org",
			want:     "global",
		},
		{
			name:     "falls back to org",
			cfg:      &NetworkConfig{KMS: &KMSConfig{}},
			orgLabel: "org",
			want:     "org",
		},
		{
			name:     "no kms block falls back to org",
			cfg:      &NetworkConfig{},
			orgLabel: "org",
			want:     "org",
		},
		{
			name:    "both missing errors",
			cfg:     &NetworkConfig{},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.ResolveKMSTokenLabel(tc.orgLabel)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
