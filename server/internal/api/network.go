package api

import (
	"net"
	"os"
	"strconv"
	"strings"
)

// netInterface is what lanURLs needs to know about a network interface.
type netInterface struct {
	name  string
	flags net.Flags
	ips   []net.IP
}

// lanURLs lists the addresses other devices on the home network can open
// Kumo at: one per network interface with a private IPv4 address, wired and
// Wi-Fi first, then the computer's .local name when Avahi announces it.
func lanURLs(port int) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var list []netInterface
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		ni := netInterface{name: ifc.Name, flags: ifc.Flags}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				ni.ips = append(ni.ips, ipn.IP)
			}
		}
		list = append(list, ni)
	}
	urls := lanURLsFrom(list, port)
	if _, err := os.Stat("/run/avahi-daemon/socket"); err == nil {
		if name, err := os.Hostname(); err == nil && name != "" {
			name, _, _ = strings.Cut(strings.ToLower(name), ".")
			urls = append(urls, "http://"+net.JoinHostPort(name+".local", strconv.Itoa(port)))
		}
	}
	return urls
}

// Interfaces other devices can't reach this computer through: VPNs,
// containers, virtual machines (Linux names, then Windows ones, lowercase).
var virtualInterfaces = []string{"docker", "br-", "veth", "virbr", "vnet", "vmnet", "vboxnet", "lxc", "lxd", "podman", "cni", "flannel", "cali", "wg", "tun", "tap", "tailscale", "zt", "nordlynx", "proton", "mullvad",
	"vethernet", "virtualbox", "vmware", "hyper-v", "npcap", "bluetooth", "loopback", "openvpn", "wireguard", "local area connection*"}

// Wired and Wi-Fi interfaces: listed first.
var physicalInterfaces = []string{"en", "eth", "wl", "wi-fi", "wifi", "wlan"}

func lanURLsFrom(ifaces []netInterface, port int) []string {
	var wired, other []string
	for _, ifc := range ifaces {
		if ifc.flags&net.FlagUp == 0 || ifc.flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		name := strings.ToLower(ifc.name)
		virtual := false
		for _, p := range virtualInterfaces {
			virtual = virtual || strings.HasPrefix(name, p) // "*" is part of the name: Wi-Fi Direct's virtual adapters
		}
		if virtual {
			continue
		}
		for _, ip := range ifc.ips {
			ip4 := ip.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			u := "http://" + net.JoinHostPort(ip4.String(), strconv.Itoa(port))
			physical := false
			for _, p := range physicalInterfaces {
				physical = physical || strings.HasPrefix(name, p)
			}
			if physical {
				wired = append(wired, u)
			} else {
				other = append(other, u)
			}
		}
	}
	return append(wired, other...)
}
