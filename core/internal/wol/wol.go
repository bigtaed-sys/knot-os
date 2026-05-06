// Package wol sends Wake-on-LAN magic packets so the user can flip a
// sleeping desktop or NAS on from the device-detail page in the
// admin UI.
//
// The magic packet format is well-known: 6 bytes of 0xff followed by
// the target MAC repeated 16 times = 102 bytes. Sent as a UDP
// broadcast, traditionally to port 9 (sometimes 7). The destination
// host's NIC firmware matches the MAC against its own and powers the
// system on; that lookup happens below the OS, so the box doesn't
// need to be running anything for it to work.
//
// The packet is sent from knotd as a directed broadcast on the LAN
// (e.g. 192.168.42.255:9) — a global broadcast (255.255.255.255)
// would also work but routers between us and the target sometimes
// drop those, while the LAN broadcast is guaranteed to reach every
// device on the segment.
package wol

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// DefaultPort is what most BIOSes / UEFI WOL implementations watch.
// 9 (discard) is the historical pick; some boards listen on 7 too.
// We use 9 unless the caller overrides.
const DefaultPort = 9

// Wake sends a magic packet for `mac` (any common MAC formatting:
// `aa:bb:cc:dd:ee:ff`, `aa-bb-cc-dd-ee-ff`, `aabbccddeeff`) to the
// broadcast address. broadcast must be the LAN's directed broadcast,
// e.g. "192.168.42.255". Port 0 falls back to DefaultPort.
//
// Returns nil on a successful UDP send. The kernel's UDP send
// silently completes — there's no application-level reply, so a
// timeout / box-still-asleep is not detectable from here.
func Wake(mac string, broadcast string, port int) error {
	hwaddr, err := parseMAC(mac)
	if err != nil {
		return fmt.Errorf("wol: %w", err)
	}
	if broadcast == "" {
		return errors.New("wol: broadcast address required")
	}
	if port == 0 {
		port = DefaultPort
	}

	packet := buildMagicPacket(hwaddr)

	dst := &net.UDPAddr{IP: net.ParseIP(broadcast), Port: port}
	if dst.IP == nil {
		return fmt.Errorf("wol: invalid broadcast %q", broadcast)
	}

	conn, err := net.DialUDP("udp4", nil, dst)
	if err != nil {
		return fmt.Errorf("wol: dial %s: %w", dst, err)
	}
	defer func() { _ = conn.Close() }()
	// We send to a directed broadcast (e.g. 192.168.42.255), not the
	// global 255.255.255.255. Linux kernels allow that without
	// SO_BROADCAST set; portability hooks for limited broadcasts
	// would be the place to add the syscall later if needed.
	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("wol: send: %w", err)
	}
	return nil
}

// buildMagicPacket assembles the 102-byte magic packet body.
func buildMagicPacket(mac net.HardwareAddr) []byte {
	out := make([]byte, 0, 6+16*6)
	for i := 0; i < 6; i++ {
		out = append(out, 0xff)
	}
	for i := 0; i < 16; i++ {
		out = append(out, mac...)
	}
	return out
}

// parseMAC accepts the most common formats: 17-char colon/hyphen
// separated, or 12-char no-separator hex.
func parseMAC(s string) (net.HardwareAddr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty MAC")
	}
	// net.ParseMAC accepts ":", "-", and "."-separated forms but not
	// the plain 12-hex form some users paste. Handle that too.
	if hw, err := net.ParseMAC(s); err == nil {
		if len(hw) != 6 {
			return nil, fmt.Errorf("MAC %q: want 6 bytes, got %d", s, len(hw))
		}
		return hw, nil
	}
	if len(s) == 12 {
		var hw net.HardwareAddr
		hw = make([]byte, 6)
		_, err := fmt.Sscanf(s, "%02x%02x%02x%02x%02x%02x",
			&hw[0], &hw[1], &hw[2], &hw[3], &hw[4], &hw[5])
		if err != nil {
			return nil, fmt.Errorf("MAC %q: %w", s, err)
		}
		return hw, nil
	}
	return nil, fmt.Errorf("MAC %q: unrecognised format", s)
}

// BroadcastForCIDR returns the directed broadcast address for a
// CIDR. "192.168.42.0/24" → "192.168.42.255". For odd prefix
// lengths (/16, /22 etc.) it correctly OR-masks the host bits.
func BroadcastForCIDR(cidr string) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	ip := ipnet.IP.To4()
	if ip == nil {
		return "", errors.New("only IPv4 CIDRs are supported")
	}
	bcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		bcast[i] = ip[i] | (^ipnet.Mask[i])
	}
	return bcast.String(), nil
}
