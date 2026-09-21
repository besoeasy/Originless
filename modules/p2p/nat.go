package p2p

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NATPortMapper attempts automatic port mapping via NAT-PMP and UPnP IGD.
type NATPortMapper struct {
	port       int
	externalIP string
	mappedPort int
	gatewayIP  string
}

// NewNATPortMapper creates a new NAT port mapping worker.
func NewNATPortMapper(port int) *NATPortMapper {
	return &NATPortMapper{
		port: port,
	}
}

// TryPortMapping attempts to map the given port via NAT-PMP and UPnP.
// Runs asynchronously and does not block if the router does not support port mapping.
func (n *NATPortMapper) TryPortMapping(ctx context.Context) {
	gateways := n.findGatewayCandidates()

	done := make(chan struct{})
	var once sync.Once

	for _, gw := range gateways {
		go func(gateway string) {
			if err := n.tryNATPMP(gateway); err == nil {
				once.Do(func() {
					log.Printf("[P2P-NAT] NAT-PMP port %d mapped successfully via gateway %s", n.port, gateway)
					close(done)
				})
			}
		}(gw)
	}

	select {
	case <-done:
		return
	case <-time.After(800 * time.Millisecond):
	case <-ctx.Done():
		return
	}

	// 2. Try UPnP SSDP discovery
	if err := n.tryUPnP(ctx); err == nil {
		log.Printf("[P2P-NAT] UPnP port %d mapped successfully", n.port)
		return
	}

	log.Printf("[P2P-NAT] Local router does not support UPnP/NAT-PMP (mesh will use DHT & direct connections)")
}

// findGatewayCandidates finds possible LAN router IPs.
func (n *NATPortMapper) findGatewayCandidates() []string {
	var candidates []string
	seen := make(map[string]bool)

	add := func(ip string) {
		if ip != "" && !seen[ip] {
			seen[ip] = true
			candidates = append(candidates, ip)
		}
	}

	// Check container hostnames
	for _, host := range []string{"host.containers.internal", "host.docker.internal"} {
		if ips, err := net.LookupIP(host); err == nil {
			for _, ip := range ips {
				if ipv4 := ip.To4(); ipv4 != nil {
					add(ipv4.String())
					// Also add .1 in that subnet
					add(fmt.Sprintf("%d.%d.%d.1", ipv4[0], ipv4[1], ipv4[2]))
				}
			}
		}
	}

	// Check local network interfaces
	if ifaces, err := net.Interfaces(); err == nil {
		for _, ifi := range ifaces {
			if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
				continue
			}
			if addrs, err := ifi.Addrs(); err == nil {
				for _, a := range addrs {
					if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.To4() != nil {
						ip := ipNet.IP.To4()
						add(fmt.Sprintf("%d.%d.%d.1", ip[0], ip[1], ip[2]))
					}
				}
			}
		}
	}

	// Common standard router IPs
	add("192.168.1.1")
	add("192.168.0.1")
	add("10.0.0.1")
	add("10.0.2.2")

	return candidates
}

// tryNATPMP attempts RFC 6886 NAT-PMP port mapping on gateway:5351.
func (n *NATPortMapper) tryNATPMP(gateway string) error {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:5351", gateway))
	if err != nil {
		return err
	}

	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))

	// Request: 12 bytes
	// Vers=0, Opcode=2 (Map TCP), Reserved=0, InternalPort, ExternalPort, Lifetime=7200
	req := make([]byte, 12)
	req[0] = 0
	req[1] = 2 // Map TCP
	binary.BigEndian.PutUint16(req[4:6], uint16(n.port))
	binary.BigEndian.PutUint16(req[6:8], uint16(n.port))
	binary.BigEndian.PutUint32(req[8:12], 7200)

	if _, err := conn.Write(req); err != nil {
		return err
	}

	resp := make([]byte, 16)
	nr, err := conn.Read(resp)
	if err != nil || nr < 16 {
		return fmt.Errorf("no NAT-PMP response")
	}

	resCode := binary.BigEndian.Uint16(resp[2:4])
	if resCode != 0 {
		return fmt.Errorf("NAT-PMP error code %d", resCode)
	}

	n.mappedPort = int(binary.BigEndian.Uint16(resp[10:12]))
	n.gatewayIP = gateway
	return nil
}

// tryUPnP discovers UPnP IGD routers via SSDP M-SEARCH and maps TCP port via SOAP.
func (n *NATPortMapper) tryUPnP(ctx context.Context) error {
	ssdpAddr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	msearch := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"ST: urn:schemas-upnp-org:service:WANIPConnection:1\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 2\r\n\r\n"

	_, _ = conn.WriteToUDP([]byte(msearch), ssdpAddr)

	buf := make([]byte, 2048)
	var locationURL string

	for {
		nr, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		respStr := string(buf[:nr])
		for _, line := range strings.Split(respStr, "\r\n") {
			if strings.HasPrefix(strings.ToUpper(line), "LOCATION:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					locationURL = strings.TrimSpace(parts[1])
					break
				}
			}
		}
		if locationURL != "" {
			break
		}
	}

	if locationURL == "" {
		return fmt.Errorf("no UPnP location received")
	}

	// Fetch device description XML
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", locationURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	controlURL := n.extractControlURL(locationURL, string(bodyBytes))
	if controlURL == "" {
		return fmt.Errorf("controlURL not found in UPnP XML")
	}

	// Send SOAP AddPortMapping
	return n.sendSOAPAddPortMapping(ctx, client, controlURL)
}

func (n *NATPortMapper) extractControlURL(baseURL, xmlStr string) string {
	type Service struct {
		ServiceType string `xml:"serviceType"`
		ControlURL  string `xml:"controlURL"`
	}
	type Device struct {
		Services []Service `xml:"serviceList>service"`
	}

	var root struct {
		XMLName xml.Name `xml:"root"`
		Device  Device   `xml:"device"`
	}

	if err := xml.Unmarshal([]byte(xmlStr), &root); err != nil {
		// Fallback simple string search
		if idx := strings.Index(xmlStr, "<controlURL>"); idx != -1 {
			end := strings.Index(xmlStr[idx:], "</controlURL>")
			if end != -1 {
				raw := xmlStr[idx+12 : idx+end]
				return resolveURL(baseURL, strings.TrimSpace(raw))
			}
		}
		return ""
	}

	for _, s := range root.Device.Services {
		if strings.Contains(s.ServiceType, "WANIPConnection") || strings.Contains(s.ServiceType, "WANPPPConnection") {
			return resolveURL(baseURL, s.ControlURL)
		}
	}

	return ""
}

func resolveURL(base, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	// Extract scheme and host from base
	parts := strings.Split(base, "/")
	if len(parts) >= 3 {
		hostPrefix := parts[0] + "//" + parts[2]
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return hostPrefix + path
	}
	return path
}

func (n *NATPortMapper) sendSOAPAddPortMapping(ctx context.Context, client *http.Client, controlURL string) error {
	soapBody := fmt.Sprintf(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body>
<u:AddPortMapping xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
<NewRemoteHost></NewRemoteHost>
<NewExternalPort>%d</NewExternalPort>
<NewProtocol>TCP</NewProtocol>
<NewInternalPort>%d</NewInternalPort>
<NewInternalClient>%s</NewInternalClient>
<NewEnabled>1</NewEnabled>
<NewPortMappingDescription>Originless P2P</NewPortMappingDescription>
<NewLeaseDuration>7200</NewLeaseDuration>
</u:AddPortMapping>
</s:Body>
</s:Envelope>`, n.port, n.port, n.localIP())

	req, err := http.NewRequestWithContext(ctx, "POST", controlURL, bytes.NewBufferString(soapBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")
	req.Header.Set("SOAPAction", `"urn:schemas-upnp-org:service:WANIPConnection:1#AddPortMapping"`)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SOAP AddPortMapping returned %s", resp.Status)
	}

	return nil
}

func (n *NATPortMapper) localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
