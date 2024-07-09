package configfile

import (
	"os"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestMakeConfigFileExample(t *testing.T) {
	XPC := XPingConfig{
		ListenHost:      "0.0.0.0",
		ListenPortStart: 32768 - 32,
		PollRateMS:      250,
		Peers: []string{
			"192.168.122.119",
			"192.168.122.96",
			// "192.168.122.49",
		},
		AllowedCIDRs: []string{
			`192.168.0.0/16`,
		},
		PeersNames: map[string]string{
			"192.168.122.119": "a",
		},
		PrometheusPort: 9150,
	}

	f, _ := os.Create("./example.yml")

	yaml.NewEncoder(f).Encode(XPC)
}

func TestMakeConfigFileLonap(t *testing.T) {
	XPC := XPingConfig{
		ListenHost:      "0.0.0.0",
		ListenPortStart: 32768 - 32,
		PollRateMS:      250,
		Peers:           []string{},
		AllowedCIDRs:    []string{},
		PeersNames:      map[string]string{},
		PrometheusPort:  9150,
	}

	a := "./lonap.json"
	lonapAutoConfigPath = &a
	XPC.LONAPAutoConfig()

	f, _ := os.Create("./example.yml")

	yaml.NewEncoder(f).Encode(XPC)
}
