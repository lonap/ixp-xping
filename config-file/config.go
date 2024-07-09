package configfile

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"gopkg.in/yaml.v2"
)

type XPingConfig struct {
	ListenHost      string
	ListenPortStart uint32
	PollRateMS      uint
	Peers           []string
	PeersNames      map[string]string
	AllowedCIDRs    []string
	PrometheusPort  uint32

	internalAllowedCIDRs []*net.IPNet
}

func Parse(in io.Reader) (XPingConfig, error) {
	XPC := XPingConfig{}
	configBytes, err := io.ReadAll(in)
	if err != nil {
		return XPC, err
	}

	err = yaml.Unmarshal(configBytes, &XPC)
	if err != nil {
		return XPC, err
	}

	return XPC, nil
}

func (XPC *XPingConfig) ResolveFriendlyName(i net.IP) string {
	if XPC.PeersNames[i.String()] != "" {
		return XPC.PeersNames[i.String()]
	}
	return i.String()
}

func (XPC *XPingConfig) IsAllowedCIDR(i net.IP) bool {
	if XPC.internalAllowedCIDRs == nil {
		XPC.internalAllowedCIDRs = make([]*net.IPNet, 0)
		for _, v := range XPC.AllowedCIDRs {
			_, c, err := net.ParseCIDR(v)
			if err == nil {
				XPC.internalAllowedCIDRs = append(XPC.internalAllowedCIDRs, c)
			} else {
				log.Printf("Invalid AlllowedCIDR %v", v)
			}
		}

		if len(XPC.AllowedCIDRs) == 0 {
			log.Printf("WARNING: There are no AllowedCIDRs set, no packets are going to be accepted!")
		}
	}

	for _, v := range XPC.internalAllowedCIDRs {
		if v.Contains(i) {
			return true
		}
	}
	return false
}

type LONAPConfigEntry struct {
	Address string
	Device  string
	Netmask string
}

var lonapAutoConfigPath = flag.String(
	"cfg.lonap-auto-config",
	"/usr/local/etc/lonap_mon_hosts.json",
	"Where to look for a LONAP style auto configuration file")

func (XPC *XPingConfig) LONAPAutoConfig() {
	XPC.PeersNames = make(map[string]string)
	lonapf, err := os.Open(*lonapAutoConfigPath)
	if err == nil {
		defer lonapf.Close()

		// Roll the peers into a map so we can avoid dupes
		PeerMap := make(map[string]bool)
		for _, v := range XPC.Peers {
			PeerMap[v] = true
		}
		AllowedCIDRsMap := make(map[string]bool)
		for _, v := range XPC.AllowedCIDRs {
			AllowedCIDRsMap[v] = true
		}

		lonapConfigStructure := make(map[string]map[string]LONAPConfigEntry)
		err = json.NewDecoder(lonapf).Decode(&lonapConfigStructure)
		if err == nil {
			for _, switches := range lonapConfigStructure {
				for switchName, switchInfo := range switches {
					XPC.PeersNames[switchInfo.Address] = switchName
					PeerMap[switchInfo.Address] = true
					AllowedCIDRsMap[quickCIDR(switchInfo.Address, switchInfo.Netmask).String()] = true
				}
			}
		} else {
			log.Printf("failed to parse lonap_mon_hosts.json: %v", err)
		}

		XPC.Peers = make([]string, 0)
		for k := range PeerMap {
			XPC.Peers = append(XPC.Peers, k)
		}
		XPC.AllowedCIDRs = make([]string, 0)
		for k := range AllowedCIDRsMap {
			XPC.AllowedCIDRs = append(XPC.AllowedCIDRs, k)
		}
	}
}

func quickCIDR(IPStr, NetmaskStr string) *net.IPNet {
	IP := net.ParseIP(IPStr)
	NM := net.ParseIP(NetmaskStr)
	NM = NM.To4()
	slash, _ := net.IPv4Mask(
		NM[0], NM[1], NM[2], NM[3]).Size()

	_, c, _ := net.ParseCIDR(fmt.Sprintf("%s/%d", IP.String(), slash))
	return c
}
