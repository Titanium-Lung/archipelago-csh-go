package Helpers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"time"

	structs "github.com/Titanium-lung/archipelago-csh-go-backend/Structs"
)

const SCRIPTS = "./pyscripts"

type TimeFormat struct {
	Data struct {
		TimeZone string `json:"timeZone"`
	} `json:"data"`
}

func DecompressAP(apPath string) (structs.ArchipelagoFile, error) {
	scriptPath := filepath.Join(SCRIPTS, "decompress_ap.py")
	cmd := exec.Command("python3", scriptPath, apPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var result structs.ArchipelagoFile
	if err := cmd.Run(); err != nil {
		return result, fmt.Errorf("python script failed: %w, stderr: %s", err, stderr.String())
	}

	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return result, fmt.Errorf("failed to parse python output as JSON: %w, raw output: %s", err, stdout.String())
	}

	return result, nil
}

func DecompressAPSave(apPath string) (structs.ArchipelagoSaveFile, error) {
	scriptPath := filepath.Join(SCRIPTS, "decompress_apsave.py")
	cmd := exec.Command("python3", scriptPath, apPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var result structs.ArchipelagoSaveFile
	if err := cmd.Run(); err != nil {
		return result, fmt.Errorf("python script failed: %w, stderr: %s", err, stderr.String())
	}

	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return result, fmt.Errorf("failed to parse python output as JSON: %w, raw output: %s", err, stdout.String())
	}

	return result, nil
}

func FormatRoomTime(t time.Time, timeZone string) (string, error) {
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return "", err
	}
	localTime := t.In(loc)

	return localTime.Format("1/2/06 3:04 pm"), nil
}

func CheckPort(port int) bool {
	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	listener.Close()
	return true
}
