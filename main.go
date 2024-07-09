package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	configfile "github.com/benjojo/ixp-xping/config-file"
	sockettimestamp "github.com/benjojo/ixp-xping/socket-timestamp"
	"github.com/oxtoacart/bpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	configFileLocation = flag.String("cfg.path", "/etc/ixp-xping.yaml", "Where to find the YAML config")
)

var runtimeConfig *configfile.XPingConfig
var bufferPool = bpool.NewBytePool(512, 10000)

func main() {
	config := configfile.XPingConfig{
		ListenHost:      "0.0.0.0",
		ListenPortStart: 32768 - 32,
		PollRateMS:      250,
		Peers:           []string{},
		AllowedCIDRs:    []string{},
		PrometheusPort:  9150,
	}
	f, err := os.Open(*configFileLocation)
	if err == nil {
		c, err := configfile.Parse(f)
		if err == nil {
			config = c
		} else {
			log.Printf("failed to parse: %v", err)
		}
	} else {
		log.Printf("did not find a config file in %v", *configFileLocation)
	}

	lowerListenPort := flag.Int("probe.lower.port", int(config.ListenPortStart),
		"The port number to start 16 UDP listeners from. The next 16 ports from the number provided should be free")
	listenHost := flag.String("probe.listen", config.ListenHost, "The IP to listen on for probes")
	flag.Parse()

	config.LONAPAutoConfig()

	runtimeConfig = &config

	err = runSanityCheck()
	if err != nil {
		log.Fatalf("Sanity check failed: %v", err)
	}
	myOwnIPs := make(map[string]bool)

	// Start the pool of UDP listeners
	// if the user is "lazy" and doing bind to *, then we will look at all interfaces and
	// listen on each one. else we can do what they say
	if runtimeConfig.ListenHost == "0.0.0.0" {
		interfaces, err := net.Interfaces()
		if err != nil {
			log.Fatalf("Could not list network interfaces, cannot intelligently bind() on ports: %v", err)
		}
		for _, iface := range interfaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}

			for _, v := range addrs {
				interfaceIP, ok := v.(*net.IPNet)

				if ok {
					myOwnIPs[interfaceIP.IP.String()] = true
					// Only listen on CIDRs that we can likely reply on anyway...
					if runtimeConfig.IsAllowedCIDR(interfaceIP.IP) {
						for i := 0; i < totalFlowsPerPeer; i++ {
							target := net.JoinHostPort(interfaceIP.IP.String(), fmt.Sprint(*lowerListenPort+i))
							uaddr, err := net.ResolveUDPAddr("udp", target)
							if err != nil {
								log.Fatalf("Could not resolve to listen on %s - %v", target, err)
							}
							udpl, err := net.ListenUDP("udp", uaddr)
							if err != nil {
								log.Fatalf("Could not listen on %s - %v", target, err)
							}

							rawFile, _ := udpl.File()
							err = syscall.BindToDevice(int(rawFile.Fd()), iface.Name)
							if err != nil {
								log.Printf("Unable to call BindToDevice, the replies may not be sent from the right interface!")
							}
							go startUDPProbeReplier(udpl)
						}
					}
				}
			}
		}
	} else {
		for i := 0; i < 16; i++ {
			target := net.JoinHostPort(*listenHost, fmt.Sprint(*lowerListenPort+i))
			uaddr, err := net.ResolveUDPAddr("udp", target)
			if err != nil {
				log.Fatalf("Could not resolve to listen on %s - %v", target, err)
			}
			udpl, err := net.ListenUDP("udp", uaddr)
			if err != nil {
				log.Fatalf("Could not listen on %s - %v", target, err)
			}
			go startUDPProbeReplier(udpl)
		}
	}

	// Now start reading our own config to see who we should be probing
	for _, srcpeer := range config.Peers {
		portAlloc := 1
		if myOwnIPs[srcpeer] {
			for _, targetPeer := range config.Peers {
				if myOwnIPs[targetPeer] {
					log.Printf("Refusing to send probes to myself (as I was configured to so)")
					continue
				}

				for i := 0; i < totalFlowsPerPeer; i++ {
					portAlloc++
					go sendPeerProbes(
						srcpeer,
						net.JoinHostPort(targetPeer, fmt.Sprint(config.ListenPortStart+uint32(i))),
						time.Duration(config.PollRateMS)*time.Millisecond,
						// this ensures that each prober has it's own, static source port, so that metrics can
						// consistent (hopefully) between xping reboots.
						config.ListenPortStart+totalFlowsPerPeer+uint32(portAlloc),
					)
					time.Sleep(time.Millisecond)
				}
			}

		}
	}

	http.Handle("/metrics", promhttp.Handler())
	log.Fatalf("failed to listen on HTTP: %v",
		http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", config.PrometheusPort), nil))
}

const (
	totalFlowsPerPeer  = 16
	lossTrackWindow    = time.Second * 10
	metricUpdateTicker = time.Second * 5
)

type peerProbeReply struct {
	buf     []byte
	arrived time.Time
}

func sendPeerProbes(sendingIP, peer string, pollRate time.Duration, localBindPort uint32) {
	poll := time.NewTicker(pollRate)
	updateMetricsTick := time.NewTicker(metricUpdateTicker)

	localListentarget := net.JoinHostPort(sendingIP, fmt.Sprint(localBindPort))
	sendingAddr, err := net.ResolveUDPAddr("udp", localListentarget)
	if err != nil {
		log.Fatalf("Cannot resolve local listen target for Peer Probe %v: %v", peer, err)
	}
	peerAddress, err := net.ResolveUDPAddr("udp", peer)
	if err != nil {
		log.Fatalf("Cannot resolve peer target for Peer %v: %v", peer, err)
	}
	sendingSocket, err := net.ListenUDP("udp", sendingAddr)
	if err != nil {
		log.Fatalf("Cannot bind to listen for probe replies for Peer %v: %v", peer, err)
	}

	// Find the interface name of the sendingIP

	interfaceName := findInterfaceNameFromIP(sendingIP)

	rawFile, _ := sendingSocket.File()
	err = syscall.BindToDevice(int(rawFile.Fd()), interfaceName)
	if err != nil {
		log.Printf("Unable to call BindToDevice, the replies may not be sent from the right interface!")
	}

	replies := make(chan probePacket, 10)

	err = sockettimestamp.EnableRXTimestampsOnSocket(sendingSocket)
	timestampingEnabled := false
	if err != nil {
		log.Printf("Unable to enable SO_TIMESTAMP on socket: %v", err)
	} else {
		timestampingEnabled = true
	}

	lossTrackingRing := PacketRingBuffer{}
	lagTrackingRing := LatencyRingBuffer{}

	// We want to track around 10 seconds of loss on avg, so
	// we will need to store 10 seconds worth of IDs in the
	// ring buffer, hence 10 s / 100 ms = 100 slots (by default)
	lossTrackingRing.Setup(int(lossTrackWindow / pollRate))
	lagTrackingRing.Setup(int(lossTrackWindow / pollRate))

	// Start RX loop that only accepts packets from the actual target
	// we are probing for.
	go func() {
		for {
			networkBuffer := bufferPool.Get()
			// networkBuffer := make([]byte, 1500)
			// oob := make([]byte, 1000)
			oob := bufferPool.Get()

			netRead, oobN, _, rxHost, err := sendingSocket.ReadMsgUDP(networkBuffer, oob)
			if err != nil {
				return // TODO; handle correctly
			}
			var now time.Time
			if timestampingEnabled {
				t, err := sockettimestamp.DecodeRXTimestampFromOOB(oob[:oobN])
				if err == nil {
					now = t
				} else {
					log.Printf("Failed to DecodeRXTimestampFromOOB: %v", err)
					now = time.Now()
				}
			} else {
				now = time.Now()
			}
			bufferPool.Put(oob)

			if !rxHost.IP.Equal(peerAddress.IP) {
				metricBadPackets.WithLabelValues(`probe-reply-wrong-ip`).Inc()
				continue
			}

			if rxHost.Port != peerAddress.Port {
				metricBadPackets.WithLabelValues(`probe-reply-wrong-port`).Inc()
				continue
			}

			replies <- unMakeProbePacket(peerProbeReply{
				buf:     networkBuffer[:netRead],
				arrived: now,
			})
			bufferPool.Put(networkBuffer)
		}
	}()

	packetN := 1 // a 64bit unit that tracks how many packets this prober has sent out

	for {
		select {
		case PP := <-replies:
			lossTrackingRing.Write(PP.Seq)
			lagTrackingRing.Write(uint64(PP.Latency.Microseconds()))
		case <-poll.C:
			packet := makeProbePacket(uint64(packetN))
			n, _, err := sendingSocket.WriteMsgUDP(
				packet[:1024],
				nil,
				peerAddress,
			)
			if n != 1024 {
				log.Printf("Failed to send full probe packet! %d (sent), %d (wanted) / %v", n, len(packet), err)
				continue
			}
			if err != nil {
				log.Fatalf("Failed to send probe packet! %v, aborting", err)
			}
			packetN++
			bufferPool.Put(packet)
		case <-updateMetricsTick.C:
			loss := lossTrackingRing.GetPacketLoss(uint64(packetN))

			metricFlowLoss.WithLabelValues(
				runtimeConfig.ResolveFriendlyName(sendingAddr.IP),
				runtimeConfig.ResolveFriendlyName(peerAddress.IP),
				fmt.Sprint(peerAddress.Port)).Set(loss)

			lag := lagTrackingRing.GetAvgLatency()
			metricFlowLatency.WithLabelValues(
				runtimeConfig.ResolveFriendlyName(sendingAddr.IP),
				runtimeConfig.ResolveFriendlyName(peerAddress.IP),
				fmt.Sprint(peerAddress.Port)).Set(float64(lag))
		}
	}
}

func findInterfaceNameFromIP(sendingIP string) (interfaceName string) {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Fatalf("Could not list network interfaces, cannot intelligently bind() on ports: %v", err)
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, v := range addrs {
			interfaceIP, ok := v.(*net.IPNet)

			if ok {
				if interfaceIP.IP.String() == sendingIP {
					interfaceName = iface.Name
				}
			}
		}
	}
	return
}

func makeProbePacket(N uint64) []byte {
	b := bufferPool.Get()
	buf := &bytes.Buffer{}
	// Out of the 1024 bytes in this, we can only use 512,
	// as the remote side will chop off half of the packet
	// when sending it back (for amp reduction design)
	buf.Write([]byte(protocolMagicValue))
	binary.Write(buf, binary.BigEndian, N)
	ts := time.Now().UnixMicro()
	binary.Write(buf, binary.BigEndian, ts)

	copy(b, buf.Bytes())
	return b
}

const (
	// 0x8330 (The protocol magic number, also 8330 is the ASN of the entity that comissioned this project)
	protocolMagicValue = "\x83\x30"
)

type probePacket struct {
	Magic   [2]byte
	Seq     uint64
	Latency time.Duration
}

func unMakeProbePacket(p peerProbeReply) probePacket {
	buf := bytes.NewBuffer(p.buf)
	// Out of the 1024 bytes in this, we can only use 512,
	// as the remote side will chop off half of the packet
	// when sending it back (for amp reduction design)

	PP := probePacket{}
	binary.Read(buf, binary.BigEndian, &PP.Magic)
	binary.Read(buf, binary.BigEndian, &PP.Seq)
	ts := uint64(0)
	binary.Read(buf, binary.BigEndian, &ts)
	PP.Latency = p.arrived.Sub(time.UnixMicro(int64(ts)))
	return PP
}

func startUDPProbeReplier(l *net.UDPConn) {
	log.Printf("Listening on %v", l.LocalAddr().String())
	defer log.Printf("No longer listening on %v", l.LocalAddr().String())
	for {
		// TODO: This (networkBuffer) could be higher with fragments
		networkBuffer := bufferPool.Get()
		// networkBuffer := make([]byte, 1500)
		finalReadSize, sourceIP, err := l.ReadFromUDP(networkBuffer)
		// captureTime := time.Now() // Capture the rx time ASAP
		if err != nil {
			break
		}

		if finalReadSize < 512 {
			// To make reflection unappealing, we require clients to send us
			// large payloads so we can send back smaller ones. This packet
			// is simply too small, and will be dropped
			metricBadPackets.WithLabelValues(`replier-too-small`).Inc()
			continue
		}

		if !runtimeConfig.IsAllowedCIDR(sourceIP.IP) {
			metricBadPackets.WithLabelValues(`replier-not-allowed-cidr`).Inc()
			continue
		}

		truePacketSlice := networkBuffer[:finalReadSize]
		inboundMagic := string(truePacketSlice[:2])
		if inboundMagic != protocolMagicValue {
			// This is not a XPING packet then, since it does not start with the protocol magic number
			metricBadPackets.WithLabelValues(`replier-no-magic`).Inc()
			continue
		}

		// Reply back with the packet, but at half the size to reduce the risk of us being used as UDP reflection.
		_, _, err = l.WriteMsgUDP(truePacketSlice[:finalReadSize/2], nil, sourceIP)
		if err != nil {
			log.Printf("debug: startUDPProbeReplier/l.WriteMsgUDP: err: %v", err)
		}
		bufferPool.Put(networkBuffer)
	}
}

func init() {
	prometheus.MustRegister(metricTotalLoss)
	prometheus.MustRegister(metricFlowLoss)
	prometheus.MustRegister(metricFlowLatency)
	prometheus.MustRegister(metricBadPackets)
}

var metricTotalLoss = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "xping_peer_loss_total",
		Help: "aaa",
	},
	[]string{"local", "peer"},
)

var metricFlowLoss = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "xping_peer_loss_per_flow",
		Help: "aaa",
	},
	[]string{"local", "peer", "port"},
)

var metricFlowLatency = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "xping_peer_latency_per_flow",
		Help: "aaa",
	},
	[]string{"local", "peer", "port"},
)

var metricBadPackets = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "xping_bad_packets",
		Help: "aaa",
	},
	[]string{"reason"},
)
