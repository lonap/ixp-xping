package main

import (
	"container/ring"
	"flag"
	"log"
	"math"
	"sync"
)

type PacketRingBuffer struct {
	storage *ring.Ring
	rl      sync.RWMutex
}

func (r *PacketRingBuffer) Setup(N int) {
	r.storage = ring.New(N)
}

const (
	// because there are likely to be packets in flight at any time, we will not look at the first
	// N of packets on the ring, as they could still be arriving
	LossGraceInflight = 2
)

var (
	debugPrintLoss = flag.Bool("debug.printloss", false, "Print details about packet loss")
)

func (r *PacketRingBuffer) GetPacketLoss(currentN uint64) float64 {
	r.rl.RLock()
	defer r.rl.RUnlock()

	// Get the length of the ring
	n := r.storage.Len()

	existsMap := make(map[uint64]uint8)

	// Iterate through the ring and store everything in the exists map for easy lookup
	for j := 0; j < n; j++ {
		if r.storage.Value != nil {
			existsMap[r.storage.Value.(uint64)] = 1
		}
		r.storage = r.storage.Next()
	}

	// now that we have a map with all of the possible values in the map, let's
	// build that average
	runningSum := 0
	for i := LossGraceInflight; i < n; i++ {
		testN := currentN - uint64(i)
		runningSum += int(existsMap[testN])
		if *debugPrintLoss {
			if int(existsMap[testN]) == 0 {
				log.Printf("%d was dropped, currentN = %d, %d apart (n = %v)", testN, currentN, currentN-testN, n)
			}
		}
	}

	if *debugPrintLoss {
		log.Printf("loss calc totalGot=%v, expected=%v", runningSum, n-LossGraceInflight)
	}
	// now make a full average, 0 = 100% loss
	rxPercent := float64(runningSum) / float64(n-LossGraceInflight)
	rxPercent = math.Abs(rxPercent - 1)
	return rxPercent
}

func (r *PacketRingBuffer) Write(packet uint64) {
	r.rl.Lock()
	defer r.rl.Unlock()

	r.storage.Value = packet
	r.storage = r.storage.Next()
}

type LatencyRingBuffer struct {
	storage *ring.Ring
	rl      sync.RWMutex
}

func (r *LatencyRingBuffer) Setup(N int) {
	r.storage = ring.New(N)
}

func (r *LatencyRingBuffer) GetAvgLatency() int {
	r.rl.RLock()
	defer r.rl.RUnlock()

	// Get the length of the ring
	n := r.storage.Len()

	runningSum := uint64(0)
	for j := 0; j < n; j++ {
		if r.storage.Value != nil {
			runningSum += r.storage.Value.(uint64)
		}
		r.storage = r.storage.Next()
	}

	// now make a full average
	avgLatency := float64(runningSum) / float64(n)
	return int(avgLatency)
}

func (r *LatencyRingBuffer) Write(latency uint64) {
	r.rl.Lock()
	defer r.rl.Unlock()

	r.storage.Value = latency
	r.storage = r.storage.Next()
}
