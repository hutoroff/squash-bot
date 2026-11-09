package discovery

import (
	"fmt"
	"net"
)

func ResolveAdvertiseHost(explicit, interfaceName string) (string, error) {
	if explicit != "" {
		ip := net.ParseIP(explicit)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return "", fmt.Errorf("SERVICE_ADVERTISE_HOST must be a non-loopback IPv4 address")
		}
		return ip.String(), nil
	}
	var interfaces []net.Interface
	if interfaceName != "" {
		selected, err := net.InterfaceByName(interfaceName)
		if err != nil {
			return "", fmt.Errorf("resolve discovery network interface: %w", err)
		}
		interfaces = []net.Interface{*selected}
	} else {
		var err error
		interfaces, err = net.Interfaces()
		if err != nil {
			return "", fmt.Errorf("list network interfaces: %w", err)
		}
	}
	var candidates []string
	for _, networkInterface := range interfaces {
		addresses, err := networkInterface.Addrs()
		if err != nil {
			return "", fmt.Errorf("list addresses for %s: %w", networkInterface.Name, err)
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || !ip.IsPrivate() {
				continue
			}
			candidates = append(candidates, ip.String())
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("discovery advertise address is ambiguous: found %d private IPv4 candidates", len(candidates))
	}
	return candidates[0], nil
}
