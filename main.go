package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"tinygo.org/x/bluetooth"
)

var Version = "dev"

var adapter = bluetooth.DefaultAdapter

func getVersion() string {
	if Version != "dev" && Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var revision string
		var modified bool
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				modified = true
			}
		}
		if revision != "" {
			if len(revision) > 7 {
				revision = revision[:7]
			}
			if modified {
				revision += "-dirty"
			}
			return revision
		}
	}
	return Version
}

type DeviceStats struct {
	MAC          string
	Name         string
	LastSeen     time.Time
	PktCount     int64
	IntervalPkts int64
	RawRSSI      int16
	SmoothedRSSI float64
	MinRSSI      int16
	MaxRSSI      int16
	SumRSSI      int64
	SampleCount  int64
}

func main() {
	showVersion := flag.Bool("v", false, "Show version and exit")
	showVersionLong := flag.Bool("version", false, "Show version and exit")
	targetMAC := flag.String("mac", "", "Target BLE MAC address (e.g. AA:BB:CC:DD:EE:FF)")
	lockRSSI := flag.Float64("lock-rssi", -85.0, "RSSI threshold (dBm) below which session locks")
	unlockRSSI := flag.Float64("unlock-rssi", -70.0, "RSSI threshold (dBm) above which session unlocks")
	timeoutSec := flag.Int("timeout", 15, "Seconds without advertisement signal before triggering lock")
	quiet := flag.Bool("quiet", false, "Quiet mode: reduce journald logs by printing status periodically (default 1m) and immediately on state changes (lock/unlock)")
	logInterval := flag.Duration("log-interval", 1*time.Minute, "Periodic status logging interval in quiet mode (e.g. 1m, 30s)")
	monitorMode := flag.Bool("monitor", false, "Run as monitoring tool to measure RSSI and test visibility in a rapid loop")
	interval := flag.Duration("interval", 1*time.Second, "Monitoring report interval in monitor mode (e.g. 1s, 500ms)")
	flag.Parse()

	if *showVersion || *showVersionLong {
		fmt.Printf("bleprox version %s\n", getVersion())
		os.Exit(0)
	}

	// Enable BLE Adapter
	if err := adapter.Enable(); err != nil {
		log.Fatalf("Failed to enable BLE adapter: %v", err)
	}

	if *monitorMode {
		runMonitorMode(*targetMAC, *interval)
		return
	}

	// Daemon Mode
	if *targetMAC == "" {
		fmt.Println("Error: -mac flag is required in daemon mode.")
		flag.Usage()
		os.Exit(1)
	}

	runDaemonMode(strings.ToLower(*targetMAC), *lockRSSI, *unlockRSSI, *timeoutSec, *quiet, *logInterval)
}

type Daemon struct {
	target       string
	lockRSSI     float64
	unlockRSSI   float64
	timeoutSec   int
	quiet        bool
	logInterval  time.Duration
	isLocked     bool
	smoothedRSSI float64
	alpha        float64
	lastSeen     time.Time
	mu           sync.Mutex
	logFunc      func(format string, v ...interface{})
	lockFunc     func(action string)
}

func NewDaemon(target string, lockRSSI, unlockRSSI float64, timeoutSec int, quiet bool, logInterval time.Duration) *Daemon {
	return &Daemon{
		target:       target,
		lockRSSI:     lockRSSI,
		unlockRSSI:   unlockRSSI,
		timeoutSec:   timeoutSec,
		quiet:        quiet,
		logInterval:  logInterval,
		isLocked:     false,
		smoothedRSSI: -70.0,
		alpha:        0.2,
		lastSeen:     time.Now(),
		logFunc:      log.Printf,
		lockFunc:     runLoginctl,
	}
}

func (d *Daemon) ProcessScan(rssi int16, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lastSeen = now
	rawRSSI := float64(rssi)
	d.smoothedRSSI = (d.alpha * rawRSSI) + ((1.0 - d.alpha) * d.smoothedRSSI)

	if !d.quiet {
		d.logFunc("MAC: %s | Raw RSSI: %d dBm | Smoothed RSSI: %.1f dBm", d.target, rssi, d.smoothedRSSI)
	}

	if !d.isLocked && d.smoothedRSSI < d.lockRSSI {
		d.logFunc("[TRIGGER] RSSI %.1f below threshold %.1f. Locking session...\n", d.smoothedRSSI, d.lockRSSI)
		d.lockFunc("lock-session")
		d.isLocked = true
	} else if d.isLocked && d.smoothedRSSI > d.unlockRSSI {
		d.logFunc("[TRIGGER] RSSI %.1f above threshold %.1f. Unlocking session...\n", d.smoothedRSSI, d.unlockRSSI)
		d.lockFunc("unlock-session")
		d.isLocked = false
	}
}

func (d *Daemon) CheckTimeout(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	elapsed := now.Sub(d.lastSeen)
	if !d.isLocked && elapsed > time.Duration(d.timeoutSec)*time.Second {
		d.logFunc("[TIMEOUT] Signal lost for >%ds (last seen %.1fs ago). Executing loginctl lock-session...\n", d.timeoutSec, elapsed.Seconds())
		d.lockFunc("lock-session")
		d.isLocked = true
	}
}

func (d *Daemon) LogStatus(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	elapsed := now.Sub(d.lastSeen)
	stateStr := "UNLOCKED"
	if d.isLocked {
		stateStr = "LOCKED"
	}
	d.logFunc("[STATUS] Target: %s | State: %s | RSSI: %.1f dBm | Last seen: %.1fs ago",
		d.target, stateStr, d.smoothedRSSI, elapsed.Seconds())
}

func runDaemonMode(target string, lockRSSI, unlockRSSI float64, timeoutSec int, quiet bool, logInterval time.Duration) {
	d := NewDaemon(target, lockRSSI, unlockRSSI, timeoutSec, quiet, logInterval)

	// Background ticker checking for missing advertisement timeout
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		for range ticker.C {
			d.CheckTimeout(time.Now())
		}
	}()

	// Periodic status logger for quiet mode
	if quiet {
		go func() {
			ticker := time.NewTicker(logInterval)
			for range ticker.C {
				d.LogStatus(time.Now())
			}
		}()
	}

	if quiet {
		log.Printf("Starting bleprox daemon for MAC: %s (Quiet mode: enabled, Log interval: %v)\n", target, logInterval)
	} else {
		log.Printf("Starting bleprox daemon for MAC: %s\n", target)
	}

	// Start continuous scan callback
	err := adapter.Scan(func(a *bluetooth.Adapter, device bluetooth.ScanResult) {
		deviceAddr := strings.ToLower(device.Address.String())
		if strings.Contains(deviceAddr, target) {
			d.ProcessScan(device.RSSI, time.Now())
		}
	})

	if err != nil {
		log.Fatalf("Scan failed: %v", err)
	}
}

func runMonitorMode(targetMAC string, interval time.Duration) {
	target := strings.ToLower(targetMAC)
	devices := make(map[string]*DeviceStats)
	var mu sync.Mutex
	alpha := 0.2
	startTime := time.Now()

	// Handle SIGINT/SIGTERM for final session summary
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		mu.Lock()
		defer mu.Unlock()

		fmt.Println("\n===============================================================")
		fmt.Println("               BLE PROXIMITY MONITOR SUMMARY                   ")
		fmt.Println("===============================================================")
		duration := time.Since(startTime).Truncate(time.Second)
		fmt.Printf("Total Monitor Duration : %v\n", duration)

		if target != "" {
			stats, found := devices[target]
			if !found || stats.SampleCount == 0 {
				fmt.Printf("Target MAC             : %s\n", targetMAC)
				fmt.Println("Status                 : DEVICE NOT DETECTED during monitoring session")
			} else {
				avgRSSI := float64(stats.SumRSSI) / float64(stats.SampleCount)
				recLock, recUnlock := calculateRecommendations(stats.MinRSSI, stats.MaxRSSI, avgRSSI)

				fmt.Printf("Target MAC             : %s (%s)\n", stats.MAC, stats.Name)
				fmt.Printf("Total Advertisements   : %d packets\n", stats.PktCount)
				fmt.Printf("RSSI Min               : %d dBm\n", stats.MinRSSI)
				fmt.Printf("RSSI Max               : %d dBm\n", stats.MaxRSSI)
				fmt.Printf("RSSI Average           : %.1f dBm\n", avgRSSI)
				fmt.Printf("Last Smoothed RSSI     : %.1f dBm\n", stats.SmoothedRSSI)
				fmt.Println("\nRECOMMENDED DAEMON COMMAND:")
				fmt.Printf("  bleprox -mac %s -lock-rssi %.1f -unlock-rssi %.1f -timeout 10\n",
					stats.MAC, recLock, recUnlock)
			}
		} else {
			fmt.Printf("Discovered Devices     : %d\n", len(devices))
			if len(devices) > 0 {
				fmt.Println("\nDiscovered Devices List:")
				for _, st := range devices {
					avgRSSI := 0.0
					if st.SampleCount > 0 {
						avgRSSI = float64(st.SumRSSI) / float64(st.SampleCount)
					}
					fmt.Printf("  • MAC: %-17s | Name: %-18s | Pkts: %-5d | Avg RSSI: %.1f dBm\n",
						st.MAC, truncateName(st.Name, 18), st.PktCount, avgRSSI)
				}
			}
		}
		fmt.Println("===============================================================")
		os.Exit(0)
	}()

	log.Printf("Starting BLE Monitor Tool (Interval: %v). Press Ctrl+C to stop.\n", interval)
	if target != "" {
		log.Printf("Monitoring target MAC: %s\n", target)
	} else {
		log.Println("No target MAC specified (-mac). Scanning all nearby BLE devices...")
	}

	// Start background scanner
	go func() {
		err := adapter.Scan(func(a *bluetooth.Adapter, device bluetooth.ScanResult) {
			addr := strings.ToLower(device.Address.String())

			// Filter if target is set
			if target != "" && !strings.Contains(addr, target) {
				return
			}

			mu.Lock()
			defer mu.Unlock()

			name := device.LocalName()
			if name == "" {
				name = "Unknown"
			}

			st, ok := devices[addr]
			if !ok {
				st = &DeviceStats{
					MAC:          device.Address.String(),
					Name:         name,
					SmoothedRSSI: float64(device.RSSI),
					MinRSSI:      device.RSSI,
					MaxRSSI:      device.RSSI,
				}
				devices[addr] = st
			}

			if name != "Unknown" && st.Name == "Unknown" {
				st.Name = name
			}

			now := time.Now()
			st.LastSeen = now
			st.PktCount++
			st.IntervalPkts++
			st.RawRSSI = device.RSSI
			st.SumRSSI += int64(device.RSSI)
			st.SampleCount++

			if device.RSSI < st.MinRSSI {
				st.MinRSSI = device.RSSI
			}
			if device.RSSI > st.MaxRSSI {
				st.MaxRSSI = device.RSSI
			}

			st.SmoothedRSSI = (alpha * float64(device.RSSI)) + ((1.0 - alpha) * st.SmoothedRSSI)
		})

		if err != nil {
			log.Fatalf("Monitor scan failed: %v", err)
		}
	}()

	// Periodic output ticker
	ticker := time.NewTicker(interval)
	for range ticker.C {
		mu.Lock()
		now := time.Now()
		timestamp := now.Format("15:04:05")

		if target != "" {
			var targetStats *DeviceStats
			for addr, st := range devices {
				if strings.Contains(addr, target) {
					targetStats = st
					break
				}
			}

			if targetStats == nil || targetStats.SampleCount == 0 {
				fmt.Printf("[%s] Target: %s | Status: NOT SEEN YET\n", timestamp, target)
			} else {
				seenInInterval := targetStats.IntervalPkts > 0
				targetStats.IntervalPkts = 0 // reset interval count
				timeSince := now.Sub(targetStats.LastSeen).Seconds()

				avgRSSI := float64(targetStats.SumRSSI) / float64(targetStats.SampleCount)
				recLock, recUnlock := calculateRecommendations(targetStats.MinRSSI, targetStats.MaxRSSI, avgRSSI)
				bar := rssiToBar(targetStats.SmoothedRSSI)

				statusStr := "SEEN"
				if !seenInInterval {
					statusStr = fmt.Sprintf("ABSENT (%.1fs ago)", timeSince)
				}

				fmt.Printf("[%s] Status: %-14s | Raw: %3d dBm | Smooth: %5.1f dBm | Range: [%3d..%3d] | %s\n",
					timestamp, statusStr, targetStats.RawRSSI, targetStats.SmoothedRSSI,
					targetStats.MinRSSI, targetStats.MaxRSSI, bar)
				fmt.Printf("           -> Rec Lock RSSI: %.1f dBm | Rec Unlock RSSI: %.1f dBm\n",
					recLock, recUnlock)
			}
		} else {
			// List top discovered devices
			type devEntry struct {
				addr string
				st   *DeviceStats
			}
			var list []devEntry
			for addr, st := range devices {
				list = append(list, devEntry{addr, st})
			}
			sort.Slice(list, func(i, j int) bool {
				return list[i].st.SmoothedRSSI > list[j].st.SmoothedRSSI
			})

			fmt.Printf("\n--- [%s] Discovered %d BLE Devices (Interval: %v) ---\n", timestamp, len(list), interval)
			if len(list) == 0 {
				fmt.Println("  No devices detected yet. Listening...")
			} else {
				for _, entry := range list {
					st := entry.st
					since := now.Sub(st.LastSeen).Seconds()
					bar := rssiToBar(st.SmoothedRSSI)
					fmt.Printf("  MAC: %-17s | Name: %-16s | RSSI: %4.1f dBm | Last Seen: %4.1fs ago | %s\n",
						st.MAC, truncateName(st.Name, 16), st.SmoothedRSSI, since, bar)
					st.IntervalPkts = 0
				}
			}
		}
		mu.Unlock()
	}
}

func rssiToBar(rssi float64) string {
	pct := (rssi + 100.0) / 60.0
	if pct < 0 {
		pct = 0
	} else if pct > 1 {
		pct = 1
	}
	totalBlocks := 10
	filled := int(math.Round(pct * float64(totalBlocks)))
	if filled < 0 {
		filled = 0
	}
	if filled > totalBlocks {
		filled = totalBlocks
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", totalBlocks-filled) + fmt.Sprintf(" %3d%%]", int(pct*100))
}

func calculateRecommendations(minRSSI, maxRSSI int16, avgRSSI float64) (lockRSSI, unlockRSSI float64) {
	// Lock threshold: below normal range when moving away
	// Set lock RSSI below min observed RSSI or average - 10 dBm
	lockRSSI = math.Min(float64(minRSSI)-3.0, avgRSSI-10.0)
	if lockRSSI > -65.0 {
		lockRSSI = -65.0
	}
	if lockRSSI < -95.0 {
		lockRSSI = -95.0
	}

	// Unlock threshold: strong signal when close to device
	// Set unlock RSSI above lock RSSI with at least 15 dBm hysteresis gap
	unlockRSSI = math.Max(float64(maxRSSI)-5.0, avgRSSI+3.0)
	if unlockRSSI < lockRSSI+15.0 {
		unlockRSSI = lockRSSI + 15.0
	}
	if unlockRSSI > -45.0 {
		unlockRSSI = -45.0
	}

	return lockRSSI, unlockRSSI
}

func truncateName(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func runLoginctl(action string) {
	cmd := exec.Command("loginctl", action)
	if err := cmd.Run(); err != nil {
		log.Printf("Failed to execute loginctl %s: %v", action, err)
	}
}

