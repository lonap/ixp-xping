package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

func runSanityCheck() error {
	if badArpIgnore() {
		return fmt.Errorf("arp ignore is not enabled, /proc/sys/net/ipv4/conf/all/arp_ignore needs to be 1")
	}

	return nil
}

func badArpIgnore() bool {
	b, err := os.ReadFile("/proc/sys/net/ipv4/conf/all/arp_ignore")
	if err != nil {
		log.Fatalf("Cannot read /proc/sys/net/ipv4/conf/all/arp_ignore: '%v', This is needed to sanity check.", err)
	}
	return strings.Contains(string(b), "0")
}
