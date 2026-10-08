package share

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"golang.org/x/net/ipv4"
)

// Kumos find each other with a small message (a beacon) sent every few
// seconds to a multicast group on every network interface. A Kumo that
// hears a new one answers it directly, so that one hears of it at once too,
// and both are found even where only one way works.
const discoveryPort = 43212

var group = net.IPv4(239, 255, 75, 77)

// beacon is the message: who sends it, and where its server is.
type beacon struct {
	Kumo    int    `json:"kumo"` // protocol version
	ID      string `json:"id"`
	Name    string `json:"name"`
	Port    int    `json:"port"`
	Key     string `json:"key"` // public key (base64)
	Version string `json:"version"`
	// Reply: an answer to a beacon, not to be answered.
	Reply bool `json:"reply,omitempty"`
	// Ask: what this Kumo shares with the one it's sent to changed: ask
	// again now.
	Ask bool `json:"ask,omitempty"`
}

const protocol = 1

type discovery struct {
	conn net.PacketConn
	pc   *ipv4.PacketConn

	mu     sync.Mutex
	joined map[string]bool // interfaces in the group
}

func listen() (*discovery, error) {
	lc := net.ListenConfig{Control: reuseAddr}
	conn, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", discoveryPort))
	if err != nil {
		return nil, err
	}
	d := &discovery{conn: conn, pc: ipv4.NewPacketConn(conn), joined: map[string]bool{}}
	_ = d.pc.SetMulticastTTL(1)         // this network only
	_ = d.pc.SetMulticastLoopback(true) // another Kumo on this computer
	d.join()
	return d, nil
}

// interfaces are the ones other computers can reach this one through.
func interfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && isLocalNetwork(ipn.IP) {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}

// join joins the group on every interface, also those that came up since.
func (d *discovery) join() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := map[string]bool{}
	for _, ifi := range interfaces() {
		now[ifi.Name] = true
		if d.joined[ifi.Name] {
			continue
		}
		if err := d.pc.JoinGroup(&ifi, &net.UDPAddr{IP: group}); err == nil {
			d.joined[ifi.Name] = true
		}
	}
	// Gone (a Wi-Fi off): joined again when it's back.
	for name := range d.joined {
		if !now[name] {
			delete(d.joined, name)
		}
	}
}

// announce sends the beacon on every interface.
func (d *discovery) announce(b beacon) {
	data, _ := json.Marshal(b)
	dst := &net.UDPAddr{IP: group, Port: discoveryPort}
	for _, ifi := range interfaces() {
		if err := d.pc.SetMulticastInterface(&ifi); err != nil {
			continue
		}
		_, _ = d.pc.WriteTo(data, nil, dst)
	}
}

// reply sends the beacon to one Kumo.
func (d *discovery) reply(b beacon, to net.IP) {
	b.Reply = true
	data, _ := json.Marshal(b)
	_, _ = d.conn.WriteTo(data, &net.UDPAddr{IP: to, Port: discoveryPort})
}

// read hands the beacons heard to handle until the discovery is closed.
func (d *discovery) read(handle func(b beacon, from net.IP)) {
	buf := make([]byte, 4096)
	for {
		n, src, err := d.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		var b beacon
		if json.Unmarshal(buf[:n], &b) != nil || b.Kumo != protocol {
			continue
		}
		if ua, ok := src.(*net.UDPAddr); ok {
			handle(b, ua.IP)
		}
	}
}

func (d *discovery) close() { _ = d.conn.Close() }

// isLocalNetwork reports addresses of the home network (or this computer):
// the only ones Kumo talks to for sharing.
func isLocalNetwork(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	// Tailscale / CGNAT 100.64.0.0/10
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64
}
