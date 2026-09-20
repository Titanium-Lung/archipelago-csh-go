package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"time"
)

func checkPort(port int) bool {
	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

func decompressAP(apPath string) (ArchipelagoFile, error) {
	cmd := exec.Command("python3", "decompress_ap.py", apPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var result ArchipelagoFile
	if err := cmd.Run(); err != nil {
		return result, fmt.Errorf("python script failed: %w, stderr: %s", err, stderr.String())
	}

	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return result, fmt.Errorf("failed to parse python output as JSON: %w, raw output: %s", err, stdout.String())
	}

	return result, nil
}

func formatRoomTime(t time.Time, timeZone string) (string, error) {
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return "", err
	}
	localTime := t.In(loc)

	return localTime.Format("1/2/06 3:04 pm"), nil
}
