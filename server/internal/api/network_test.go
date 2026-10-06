package api

import (
	"net"
	"reflect"
	"testing"
)

func TestLanURLs(t *testing.T) {
	up := net.FlagUp | net.FlagBroadcast
	ifaces := []netInterface{
		{"lo", net.FlagUp | net.FlagLoopback, []net.IP{net.ParseIP("127.0.0.1")}},
		{"docker0", up, []net.IP{net.ParseIP("172.17.0.1")}},
		{"wg0-mullvad", net.FlagUp | net.FlagPointToPoint, []net.IP{net.ParseIP("10.64.12.3")}},
		{"virbr0", up, []net.IP{net.ParseIP("192.168.122.1")}},
		{"br0", up, []net.IP{net.ParseIP("192.168.1.5")}},
		{"enp7s0", up, []net.IP{net.ParseIP("192.168.40.15"), net.ParseIP("fe80::1"), net.ParseIP("2001:db8::5")}},
		{"wlan0", 0, []net.IP{net.ParseIP("192.168.40.16")}}, // down
		{"tailscale0", up, []net.IP{net.ParseIP("100.101.2.3")}},
		{"eth1", up, []net.IP{net.ParseIP("8.8.4.4")}}, // not a home network
	}
	got := lanURLsFrom(ifaces, 43211)
	want := []string{"http://192.168.40.15:43211", "http://192.168.1.5:43211"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Windows names its adapters differently.
	windows := []netInterface{
		{"vEthernet (WSL (Hyper-V firewall))", up, []net.IP{net.ParseIP("172.28.16.1")}},
		{"VirtualBox Host-Only Network", up, []net.IP{net.ParseIP("192.168.56.1")}},
		{"Ethernet", up, []net.IP{net.ParseIP("192.168.40.20")}},
		{"Wi-Fi", up, []net.IP{net.ParseIP("10.0.0.12")}},
		{"Local Area Connection* 2", up, []net.IP{net.ParseIP("192.168.137.1")}}, // Wi-Fi Direct
		{"Local Area Connection", up, []net.IP{net.ParseIP("192.168.50.7")}},     // a real adapter, old naming
		{"Loopback Pseudo-Interface 1", net.FlagUp | net.FlagLoopback, []net.IP{net.ParseIP("127.0.0.1")}},
	}
	got = lanURLsFrom(windows, 43211)
	want = []string{"http://192.168.40.20:43211", "http://10.0.0.12:43211", "http://192.168.50.7:43211"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Windows: got %v, want %v", got, want)
	}
}
