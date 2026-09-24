package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDaemonQuietMode(t *testing.T) {
	var logs []string
	var lockActions []string

	d := NewDaemon("aa:bb:cc:dd:ee:ff", -85.0, -70.0, 10, true, 1*time.Minute)
	d.logFunc = func(format string, v ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, v...))
	}
	d.lockFunc = func(action string) {
		lockActions = append(lockActions, action)
	}

	now := time.Now()

	// Normal packet in quiet mode should NOT produce per-packet logs
	d.ProcessScan(-65, now)
	if len(logs) != 0 {
		t.Errorf("Expected 0 logs for normal scan in quiet mode, got %d: %v", len(logs), logs)
	}

	// RSSI drops below lock threshold (-85.0) -> should log [TRIGGER] and execute lock-session immediately
	d.ProcessScan(-90, now) // smoothed will be 0.2*-90 + 0.8*-69 = -73.2
	// Send more low RSSI packets to lower smoothed RSSI below -85.0
	for i := 0; i < 10; i++ {
		d.ProcessScan(-90, now)
	}

	if len(lockActions) == 0 || lockActions[0] != "lock-session" {
		t.Errorf("Expected lock-session action triggered, got %v", lockActions)
	}

	if len(logs) == 0 || !strings.Contains(logs[0], "[TRIGGER]") {
		t.Errorf("Expected immediate [TRIGGER] log on lock state change, got logs: %v", logs)
	}

	// Reset log slice
	logs = nil
	lockActions = nil

	// RSSI goes back above unlock threshold (-70.0) -> should log [TRIGGER] and unlock-session
	for i := 0; i < 15; i++ {
		d.ProcessScan(-50, now)
	}

	if len(lockActions) == 0 || lockActions[0] != "unlock-session" {
		t.Errorf("Expected unlock-session action triggered, got %v", lockActions)
	}

	if len(logs) == 0 || !strings.Contains(logs[0], "[TRIGGER]") {
		t.Errorf("Expected immediate [TRIGGER] log on unlock state change, got logs: %v", logs)
	}
}

func TestDaemonTimeout(t *testing.T) {
	var logs []string
	var lockActions []string

	d := NewDaemon("aa:bb:cc:dd:ee:ff", -85.0, -70.0, 10, true, 1*time.Minute)
	d.logFunc = func(format string, v ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, v...))
	}
	d.lockFunc = func(action string) {
		lockActions = append(lockActions, action)
	}

	start := time.Now()
	d.ProcessScan(-65, start)

	// Check timeout before timeoutSec (e.g. 5 seconds later) -> no action
	d.CheckTimeout(start.Add(5 * time.Second))
	if len(lockActions) != 0 {
		t.Errorf("Expected no lock action at 5s, got %v", lockActions)
	}

	// Check timeout after timeoutSec (e.g. 11 seconds later) -> lock action
	d.CheckTimeout(start.Add(11 * time.Second))
	if len(lockActions) != 1 || lockActions[0] != "lock-session" {
		t.Errorf("Expected lock-session at 11s, got %v", lockActions)
	}

	if len(logs) == 0 || !strings.Contains(logs[0], "[TIMEOUT]") {
		t.Errorf("Expected [TIMEOUT] log, got %v", logs)
	}
}

func TestDaemonStatusLog(t *testing.T) {
	var logs []string

	d := NewDaemon("aa:bb:cc:dd:ee:ff", -85.0, -70.0, 10, true, 1*time.Minute)
	d.logFunc = func(format string, v ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, v...))
	}

	now := time.Now()
	d.ProcessScan(-72, now)
	d.LogStatus(now.Add(30 * time.Second))

	if len(logs) != 1 || !strings.Contains(logs[0], "[STATUS]") || !strings.Contains(logs[0], "UNLOCKED") {
		t.Errorf("Unexpected status log: %v", logs)
	}
}

func TestGetVersion(t *testing.T) {
	v := getVersion()
	if v == "" {
		t.Errorf("Expected non-empty version string, got empty")
	}

	origVersion := Version
	defer func() { Version = origVersion }()

	Version = "v1.2.3"
	if getVersion() != "v1.2.3" {
		t.Errorf("Expected v1.2.3, got %s", getVersion())
	}
}
